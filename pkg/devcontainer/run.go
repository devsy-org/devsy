package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/drivercreate"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/language"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

type Runner interface {
	Up(ctx context.Context, options UpOptions, timeout time.Duration) (*config.Result, error)

	Build(ctx context.Context, options provider2.BuildOptions) (string, error)

	Find(ctx context.Context) (*config.ContainerDetails, error)

	Command(
		ctx context.Context,
		user string,
		command string,
		stdin io.Reader,
		stdout io.Writer,
		stderr io.Writer,
	) error

	Stop(ctx context.Context) error

	Delete(ctx context.Context) error

	Logs(ctx context.Context, writer io.Writer) error
}

func NewRunner(
	agentPath, agentDownloadURL string,
	workspaceConfig *provider2.AgentWorkspaceInfo,
) (Runner, error) {
	driver, err := drivercreate.NewDriver(workspaceConfig)
	if err != nil {
		return nil, err
	}

	// we use the workspace uid as id to avoid conflicts between container names
	return &runner{
		Driver: driver,

		AgentPath:            agentPath,
		AgentDownloadURL:     agentDownloadURL,
		LocalWorkspaceFolder: workspaceConfig.ContentFolder,
		ID:                   GetRunnerIDFromWorkspace(workspaceConfig.Workspace),
		WorkspaceConfig:      workspaceConfig,
	}, nil
}

type runner struct {
	Driver driver.Driver

	WorkspaceConfig  *provider2.AgentWorkspaceInfo
	AgentPath        string
	AgentDownloadURL string

	LocalWorkspaceFolder string

	ID string
}

type UpOptions struct {
	provider2.CLIOptions

	NoBuild       bool
	ForceBuild    bool
	RegistryCache string
}

func (r *runner) Up(
	ctx context.Context,
	options UpOptions,
	timeout time.Duration,
) (*config.Result, error) {
	log.Debugf(
		"Up devcontainer for workspace '%s' with timeout %s",
		r.WorkspaceConfig.Workspace.ID,
		timeout,
	)

	substitutedConfig, substitutionContext, err := r.getSubstitutedConfig(options.CLIOptions)
	if err != nil {
		return nil, err
	}
	defer cleanupBuildInformation(substitutedConfig.Config)

	// do not run initialize command in platform mode
	if !options.Platform.Enabled {
		if err := runInitializeCommand(
			r.LocalWorkspaceFolder,
			substitutedConfig.Config,
			options.InitEnv,
		); err != nil {
			return nil, err
		}
	} else if len(substitutedConfig.Config.InitializeCommand) > 0 {
		log.Info("Skipping initializeCommand on platform")
	}

	switch {
	case isDockerFileConfig(substitutedConfig.Config),
		substitutedConfig.Config.Image != "",
		substitutedConfig.Config.ContainerID != "":
		return r.runSingleContainer(
			ctx,
			substitutedConfig,
			substitutionContext,
			options,
			timeout,
		)
	case isDockerComposeConfig(substitutedConfig.Config):
		return r.runDockerCompose(ctx, substitutedConfig, substitutionContext, options, timeout)
	default:
		return r.runDefaultContainer(ctx, options, substitutedConfig, substitutionContext, timeout)
	}
}

func (r *runner) runDefaultContainer(
	ctx context.Context,
	options UpOptions,
	substitutedConfig *config.SubstitutedConfig,
	substitutionContext *config.SubstitutionContext,
	timeout time.Duration,
) (*config.Result, error) {
	if options.FallbackImage != "" {
		log.Warn(
			"dev container config is missing one of \"image\", \"dockerFile\" or \"dockerComposeFile\" properties, " +
				"using fallback image " + options.FallbackImage,
		)

		substitutedConfig.Config.ImageContainer = config.ImageContainer{
			Image: options.FallbackImage,
		}
	} else {
		log.Warn(
			"dev container config is missing one of \"image\", \"dockerFile\" or \"dockerComposeFile\" properties, " +
				"defaulting to auto-detection",
		)

		lang, err := language.DetectLanguage(r.LocalWorkspaceFolder)
		if err != nil {
			return nil, fmt.Errorf(
				"could not detect project language and dev container config is missing one of " +
					"\"image\", \"dockerFile\" or \"dockerComposeFile\" properties",
			)
		}

		if language.MapConfig[lang] == nil {
			return nil, fmt.Errorf(
				"could not detect project language and dev container config is missing one of " +
					"\"image\", \"dockerFile\" or \"dockerComposeFile\" properties",
			)
		}
		substitutedConfig.Config.ImageContainer = language.MapConfig[lang].ImageContainer
	}

	return r.runSingleContainer(ctx, substitutedConfig, substitutionContext, options, timeout)
}

func (r *runner) Command(
	ctx context.Context,
	user string,
	command string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return r.Driver.CommandDevContainer(ctx, r.ID, user, command, stdin, stdout, stderr)
}

func (r *runner) Find(ctx context.Context) (*config.ContainerDetails, error) {
	containerDetails, err := r.Driver.FindDevContainer(ctx, r.ID)
	if err != nil {
		return nil, fmt.Errorf("find dev container: %w", err)
	}

	return containerDetails, nil
}

func (r *runner) Logs(ctx context.Context, writer io.Writer) error {
	return r.Driver.GetDevContainerLogs(ctx, r.ID, writer, writer)
}

func isDockerFileConfig(config *config.DevContainerConfig) bool {
	return config.GetDockerfile() != ""
}

// initCmdContext groups the shared state for running initializeCommand
// sub-commands.
type initCmdContext struct {
	shellArgs       []string
	workspaceFolder string
	extraEnvVars    []string
}

func runInitializeCommand(
	workspaceFolder string,
	config *config.DevContainerConfig,
	extraEnvVars []string,
) error {
	if len(config.InitializeCommand) == 0 {
		return nil
	}

	ctx := initCmdContext{
		shellArgs:       []string{"sh", "-c"},
		workspaceFolder: workspaceFolder,
		extraEnvVars:    extraEnvVars,
	}
	// According to the devcontainer spec, `initializeCommand` needs to be run on the host.
	// On Windows we can't assume everyone has `sh` added to their PATH so we need to use Windows default shell (usually cmd.exe)
	if runtime.GOOS == "windows" {
		comSpec := os.Getenv("COMSPEC")
		if comSpec != "" {
			ctx.shellArgs = []string{comSpec, "/c"}
		} else {
			ctx.shellArgs = []string{"cmd.exe", "/c"}
		}
	}

	// When the hook has multiple named keys (object syntax), run
	// sub-commands concurrently per the devcontainer spec, matching
	// executeLifecycleHook in lifecyclehooks.go.
	if len(config.InitializeCommand) > 1 {
		return ctx.runParallel(config.InitializeCommand)
	}

	for name, cmd := range config.InitializeCommand {
		return ctx.runSingle(name, cmd)
	}

	return nil
}

func (c *initCmdContext) runParallel(
	commands map[string][]string,
) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	wg.Add(len(commands))
	for name, cmd := range commands {
		go func() {
			defer wg.Done()
			if err := c.runSingle(name, cmd); err != nil {
				mu.Lock()
				errs = append(
					errs,
					fmt.Errorf("named command %q failed: %w", name, err),
				)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return errors.Join(errs...)
}

func (c *initCmdContext) runSingle(
	name string,
	cmd []string,
) error {
	var args []string
	if len(cmd) == 1 {
		args = []string{
			c.shellArgs[0],
			c.shellArgs[1],
			cmd[0],
		}
	} else {
		args = cmd
	}

	log.Infof(
		"Running initializeCommand from devcontainer.json: %s '%s'",
		name,
		strings.Join(args, " "),
	)
	writer := log.Writer(log.LevelInfo)
	errwriter := log.Writer(log.LevelError)
	defer func() { _ = writer.Close() }()
	defer func() { _ = errwriter.Close() }()

	execCmd := exec.Command(args[0], args[1:]...)
	env := execCmd.Environ()
	env = append(env, c.extraEnvVars...)

	execCmd.Stdout = writer
	execCmd.Stderr = errwriter
	execCmd.Dir = c.workspaceFolder
	execCmd.Env = env

	return execCmd.Run()
}

func getWorkspace(
	workspaceFolder, workspaceID string,
	conf *config.DevContainerConfig,
) (string, string) {
	if conf.WorkspaceMount != "" {
		mount := config.ParseMount(conf.WorkspaceMount)
		return conf.WorkspaceMount, mount.Target
	}

	containerMountFolder := conf.WorkspaceFolder
	if containerMountFolder == "" {
		containerMountFolder = "/workspaces/" + workspaceID
	}

	consistency := ""
	if runtime.GOOS != "linux" {
		consistency = ",consistency='consistent'"
	}

	return fmt.Sprintf(
		"type=bind,source=%s,target=%s%s",
		workspaceFolder,
		containerMountFolder,
		consistency,
	), containerMountFolder
}

func GetRunnerIDFromWorkspace(workspace *provider2.Workspace) string {
	ID := workspace.UID
	if encoding.IsLegacyUID(workspace.UID) {
		ID = workspace.ID
	}

	return ID
}
