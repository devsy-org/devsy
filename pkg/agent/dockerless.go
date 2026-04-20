package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/copy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/envfile"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

const (
	DockerlessEnvVar          = "DOCKERLESS"
	DockerlessContextEnvVar   = "DOCKERLESS_CONTEXT"
	DefaultImageConfigPath    = "/.dockerless/image.json"
	DockerlessCredentialsPath = "/.dockerless/.docker" // #nosec G101 -- not a credential
	trueValue                 = "true"
)

type ConfigureCredentialsFunc func(context.Context) (string, error)

type DockerlessBuildOptions struct {
	Context                  context.Context
	SetupInfo                *config.Result
	DockerlessOptions        *provider2.ProviderDockerlessOptions
	ImageConfigOutput        string
	Debug                    bool
	ConfigureCredentialsFunc ConfigureCredentialsFunc
}

func IsDockerlessEnabled() bool {
	return os.Getenv(DockerlessEnvVar) == trueValue
}

func GetDockerlessBuildContext() string {
	return os.Getenv(DockerlessContextEnvVar)
}

func ImageConfigExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func DockerlessBuild(opts DockerlessBuildOptions) error {
	if err := validateBuildOptions(opts); err != nil {
		return err
	}

	if !shouldBuild(opts) {
		return nil
	}

	return executeBuild(opts)
}

func executeBuild(opts DockerlessBuildOptions) error {
	buildContext := GetDockerlessBuildContext()
	if err := prepareBuildDirectory(buildContext); err != nil {
		return err
	}
	defer cleanupBuildDirectory(buildContext)

	binaryPath, err := os.Executable()
	if err != nil {
		return err
	}

	cleanup := setupDockerCredentials(opts)
	if cleanup != nil {
		defer cleanup()
	}

	args := buildDockerlessArgs(binaryPath, opts)

	if err := runDockerlessBuild(opts.Context, args, opts.Debug); err != nil {
		return err
	}

	return applyContainerEnv(opts.ImageConfigOutput)
}

func validateBuildOptions(opts DockerlessBuildOptions) error {
	if opts.SetupInfo == nil {
		return fmt.Errorf("setup info is required for dockerless build")
	}
	if opts.DockerlessOptions == nil {
		return fmt.Errorf("dockerless options are required for dockerless build")
	}
	if opts.Context == nil {
		return fmt.Errorf("context is required for dockerless build")
	}
	return nil
}

func shouldBuild(opts DockerlessBuildOptions) bool {
	if !IsDockerlessEnabled() {
		return false
	}

	if ImageConfigExists(opts.ImageConfigOutput) {
		log.Debugf("skip dockerless build, because container was built already")
		return false
	}

	buildContext := GetDockerlessBuildContext()
	if buildContext == "" {
		log.Debugf("build context is missing for dockerless build")
		return false
	}

	return true
}

func prepareBuildDirectory(buildContext string) error {
	fallbackDir := filepath.Join(
		config.DevsyDockerlessBuildInfoFolder,
		config.DevsyContextFeatureFolder,
	)
	buildInfoDir := filepath.Join(buildContext, config.DevsyContextFeatureFolder)

	if _, err := os.Stat(buildInfoDir); os.IsNotExist(err) {
		if err := copy.RenameDirectory(fallbackDir, buildInfoDir); err != nil {
			return fmt.Errorf("rename dir: %w", err)
		}

		if _, err := os.Stat(buildInfoDir); err != nil {
			return fmt.Errorf("couldn't find build dir %s: %w", buildInfoDir, err)
		}
	}

	return nil
}

func setupDockerCredentials(opts DockerlessBuildOptions) func() {
	if opts.DockerlessOptions.DisableDockerCredentials == trueValue {
		log.Debugf("docker credentials disabled via DisableDockerCredentials option")
		return nil
	}

	if opts.ConfigureCredentialsFunc == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(opts.Context)
	originalPath := os.Getenv("PATH")
	originalDockerConfig := os.Getenv("DOCKER_CONFIG")
	dockerCredentialsDir, err := opts.ConfigureCredentialsFunc(ctx)
	if err != nil {
		cancel()
		_ = os.Setenv("PATH", originalPath)
		if originalDockerConfig != "" {
			_ = os.Setenv("DOCKER_CONFIG", originalDockerConfig)
		} else {
			_ = os.Unsetenv("DOCKER_CONFIG")
		}
		log.Warnf(
			"failed to configure docker credentials, private registries may not work: %v",
			err,
		)
		return nil
	}

	return func() {
		cancel()
		if originalDockerConfig != "" {
			_ = os.Setenv("DOCKER_CONFIG", originalDockerConfig)
		} else {
			_ = os.Unsetenv("DOCKER_CONFIG")
		}
		_ = os.Setenv("PATH", originalPath)
		_ = os.RemoveAll(dockerCredentialsDir)
	}
}

func cleanupBuildDirectory(buildContext string) {
	fallbackDir := filepath.Join(
		config.DevsyDockerlessBuildInfoFolder,
		config.DevsyContextFeatureFolder,
	)
	buildInfoDir := filepath.Join(buildContext, config.DevsyContextFeatureFolder)

	_ = os.RemoveAll(fallbackDir)
	if err := copy.RenameDirectory(buildInfoDir, fallbackDir); err != nil {
		log.Debugf("error renaming dir %s: %v", buildInfoDir, err)
	}
}

func buildDockerlessArgs(binaryPath string, opts DockerlessBuildOptions) []string {
	args := []string{"build", "--ignore-path", binaryPath}
	args = append(args, parseIgnorePaths(opts.DockerlessOptions.IgnorePaths)...)
	args = append(args, "--build-arg", "TARGETOS="+runtime.GOOS)
	args = append(args, "--build-arg", "TARGETARCH="+runtime.GOARCH)

	if opts.DockerlessOptions.RegistryCache != "" {
		log.Debugf(
			"appending registry cache to dockerless build arguments: %v",
			opts.DockerlessOptions.RegistryCache,
		)
		args = append(args, "--registry-cache", opts.DockerlessOptions.RegistryCache)
	}

	if opts.SetupInfo.SubstitutionContext.ContainerWorkspaceFolder != "" {
		args = append(
			args,
			"--ignore-path",
			opts.SetupInfo.SubstitutionContext.ContainerWorkspaceFolder,
		)
	}
	for _, m := range opts.SetupInfo.MergedConfig.Mounts {
		if m.Target != "" {
			if files, err := os.ReadDir(m.Target); err == nil && len(files) > 0 {
				args = append(args, "--ignore-path", m.Target)
			}
		}
	}

	return args
}

func parseIgnorePaths(ignorePaths string) []string {
	if strings.TrimSpace(ignorePaths) == "" {
		return nil
	}

	var retPaths []string
	for s := range strings.SplitSeq(ignorePaths, ",") {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		retPaths = append(retPaths, "--ignore-path", trimmed)
	}

	return retPaths
}

func runDockerlessBuild(ctx context.Context, args []string, debug bool) error {
	errWriter := log.Writer(log.LevelInfo)
	defer func() { _ = errWriter.Close() }()

	var stderrBuf bytes.Buffer
	stderrWriter := io.MultiWriter(errWriter, &stderrBuf)

	cmd := exec.CommandContext(ctx, "/.dockerless/dockerless", args...)
	if debug {
		debugWriter := log.Writer(log.LevelDebug)
		defer func() { _ = debugWriter.Close() }()
		cmd.Stdout = debugWriter
	}

	cmd.Stderr = stderrWriter
	cmd.Env = os.Environ()

	log.Infof(
		"starting dockerless build: %s %s",
		"/.dockerless/dockerless",
		strings.Join(args, " "),
	)
	if err := cmd.Run(); err != nil {
		stderrOutput := strings.TrimSpace(stderrBuf.String())
		log.Errorf("dockerless build failed: %v: stderr output: %s", err, stderrOutput)
		return err
	}

	log.Debugf("dockerless build completed")
	return nil
}

func applyContainerEnv(imageConfigPath string) error {
	// #nosec G304 -- imageConfigPath is controlled by the application, not user input
	rawConfig, err := os.ReadFile(imageConfigPath)
	if err != nil {
		return err
	}

	configFile := &v1.ConfigFile{}
	if err := json.Unmarshal(rawConfig, configFile); err != nil {
		return fmt.Errorf("parse container config: %w", err)
	}

	envfile.MergeAndApply(config.ListToObject(configFile.Config.Env))
	return nil
}
