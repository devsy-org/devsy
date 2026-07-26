package workspace

import (
	"context"
	"fmt"
	"os"

	cmdflags "github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/provider"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// BuildCmd holds the cmd flags.
type BuildCmd struct {
	*cmdflags.GlobalFlags
	provider.CLIOptions

	ProviderOptions []string

	SkipDelete bool
	Machine    string
}

// NewBuildCmd creates a new command.
func NewBuildCmd(flags *cmdflags.GlobalFlags) *cobra.Command {
	cmd := &BuildCmd{
		GlobalFlags: flags,
	}
	buildCmd := &cobra.Command{
		Use:   "build [flags] [workspace-path|workspace-name]",
		Short: "Build a workspace image",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.execute(cobraCmd.Context(), args)
		},
	}

	cmd.registerFlags(buildCmd)
	buildCmd.MarkFlagsMutuallyExclusive(names.NoLockfile, names.FrozenLockfile)
	return buildCmd
}

func (cmd *BuildCmd) Run(ctx context.Context, client client.WorkspaceClient) error {
	return cmd.build(ctx, client)
}

func (cmd *BuildCmd) registerFlags(buildCmd *cobra.Command) {
	cmd.registerDevContainerFlags(buildCmd)
	cmd.registerImageFlags(buildCmd)
	cmd.registerTestingFlags(buildCmd)
}

func (cmd *BuildCmd) registerDevContainerFlags(buildCmd *cobra.Command) {
	cliflags.RegisterDevContainerModifierFlags(buildCmd.Flags(), cliflags.DevContainerModifierFlags{
		Image:    &cmd.DevContainerImage,
		Features: &cmd.AdditionalFeatures,
	})
	cliflags.Add(buildCmd,
		cliflags.String(&cmd.DevContainerPath, names.DevContainerPath, "",
			"The path to the devcontainer.json relative to the project"),
		cliflags.StringSlice(&cmd.ProviderOptions, names.ProviderOption, nil,
			"Provider option in the form KEY=VALUE"),
		cliflags.Value(&cmd.GitCloneStrategy, names.GitCloneStrategy,
			"The git clone strategy Devsy uses to checkout git based workspaces. "+
				"Can be full (default), blobless, treeless or shallow"),
		cliflags.Bool(&cmd.GitCloneRecursiveSubmodules, names.GitRecurseSubmodules, false,
			"Clone submodules recursively"),
		cliflags.Value(&cmd.GitLFSMode, names.GitLFSMode,
			"How Devsy handles Git LFS after cloning. Can be full (default, download LFS "+
				"content), setup-only (configure LFS but leave pointer files) or skip (ignore LFS)"),
	)
}

func (cmd *BuildCmd) registerImageFlags(buildCmd *cobra.Command) {
	cliflags.Add(
		buildCmd,
		cliflags.Bool(&cmd.SkipDelete, names.SkipDelete, false,
			"If true will not delete the workspace after building it"),
		cliflags.String(&cmd.Machine, names.Machine, "",
			"The machine to use for this workspace. The machine needs to exist beforehand or the "+
				"command will fail. If the workspace already exists, this option has no effect"),
		cliflags.String(&cmd.Repository, names.Repository, "", "The repository to push to"),
		cliflags.StringSlice(&cmd.Tag, names.Tag, nil,
			"Image Tag(s) in the form of a comma separated list --tag latest,arm64 or "+
				"multiple flags --tag latest --tag arm64"),
		cliflags.StringSlice(&cmd.Platforms, names.Platform, nil, "Set target platform for build"),
		cliflags.Bool(&cmd.SkipPush, names.SkipPush, false,
			"If true will not push the image to the repository, useful for testing"),
		cliflags.Bool(&cmd.PushDuringBuild, names.Push, false,
			"Push image directly to registry during build, skipping load to local daemon"),
		cliflags.StringArray(
			&cmd.CacheFrom,
			names.CacheFrom,
			nil,
			"Cache sources for the build (e.g., myregistry.io/cache:latest or type=registry,ref=...). "+
				"Takes priority over devcontainer.json build.cacheFrom",
		),
		cliflags.Bool(&cmd.NoCache, names.NoCache, false, "Disable Docker build cache"),
		cliflags.StringArray(&cmd.Labels, names.Label, nil,
			"Add labels to the built image (format: key=value, can be specified multiple times)"),
		cliflags.String(&cmd.Output, names.Output, "", "Build output type (docker or oci)"),
		cliflags.String(&cmd.ExperimentalLockfile, names.ExperimentalLockfile, "",
			"Lockfile path for reproducible builds"),
		cliflags.Bool(&cmd.FrozenLockfile, names.FrozenLockfile, false,
			"Fail if devcontainer-lock.json is missing or does not match the resolved features "+
				"instead of writing it"),
		cliflags.Bool(&cmd.NoLockfile, names.NoLockfile, false,
			"Disable devcontainer-lock.json generation and verification"),
		cliflags.String(
			&cmd.ImageName,
			names.ImageName,
			"",
			"Alternative name for the built image",
		),
		cliflags.Bool(&cmd.NoBuild, names.NoBuild, false,
			"Fail if the image must be built (enforce pre-built images only)"),
		cliflags.Bool(&cmd.Pull, names.Pull, false,
			"Always attempt to pull a newer version of the base image when building"),
	)
	buildCmd.MarkFlagsMutuallyExclusive(names.Push, names.SkipPush)
}

func (cmd *BuildCmd) registerTestingFlags(buildCmd *cobra.Command) {
	cliflags.Add(
		buildCmd,
		cliflags.Bool(&cmd.ForceBuild, names.ForceBuild, false, "TESTING ONLY").Hidden(),
		cliflags.Bool(&cmd.ForceInternalBuildKit, names.ForceInternalBuildKit, false, "TESTING ONLY").
			Hidden(),
	)
}

func (cmd *BuildCmd) execute(ctx context.Context, args []string) error {
	devsyConfig, err := cmd.prepareBuild(ctx)
	if err != nil {
		return err
	}

	exists := workspace2.Exists(ctx, devsyConfig, args, "", cmd.Owner)
	sshConfigPath, cleanup, err := NewTempSSHConfig()
	if err != nil {
		return err
	}
	defer cleanup()

	baseWorkspaceClient, err := workspace2.Resolve(ctx, devsyConfig, workspace2.ResolveParams{
		Args:                args,
		DesiredMachine:      cmd.Machine,
		ProviderUserOptions: cmd.ProviderOptions,
		DevContainerImage:   cmd.DevContainerImage,
		DevContainerPath:    cmd.DevContainerPath,
		SSHConfigPath:       sshConfigPath,
		UID:                 cmd.UID,
		Owner:               cmd.Owner,
	})
	if err != nil {
		return err
	}

	if exists == "" && !cmd.SkipDelete {
		defer cmd.cleanupTempWorkspace(ctx, baseWorkspaceClient)
	}

	workspaceClient, ok := baseWorkspaceClient.(client.WorkspaceClient)
	if !ok {
		return fmt.Errorf("building is currently not supported for proxy providers")
	}

	return cmd.Run(ctx, workspaceClient)
}

// prepareBuild loads config and validates flags/permissions before resolving a workspace.
func (cmd *BuildCmd) prepareBuild(ctx context.Context) (*config.Config, error) {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return nil, err
	}
	if err := cmd.validateBuildFlags(); err != nil {
		return nil, err
	}
	if err := cmd.checkPushPermissions(ctx); err != nil {
		return nil, err
	}
	if len(cmd.Tag) > 0 {
		if err := image.ValidateTags(cmd.Tag); err != nil {
			return nil, fmt.Errorf("cannot build image: %w", err)
		}
	}
	if devsyConfig.ContextOptionBool(config.ContextOptionSSHStrictHostKeyChecking) {
		cmd.StrictHostKeyChecking = true
	}
	return devsyConfig, nil
}

func (cmd *BuildCmd) validateBuildFlags() error {
	if cmd.PushDuringBuild && cmd.Repository == "" {
		return fmt.Errorf("%s requires %s to be specified",
			names.Flag(names.Push), names.Flag(names.Repository))
	}
	return nil
}

func (cmd *BuildCmd) checkPushPermissions(ctx context.Context) error {
	if cmd.SkipPush || cmd.Repository == "" {
		return nil
	}
	if err := image.CheckPushPermissions(ctx, cmd.Repository); err != nil {
		return fmt.Errorf(
			"cannot push to %s, make sure you have push permissions to repository: %w",
			cmd.Repository, err,
		)
	}
	return nil
}

func (cmd *BuildCmd) cleanupTempWorkspace(
	ctx context.Context, c client.BaseWorkspaceClient,
) {
	if err := c.Delete(ctx, client.DeleteOptions{Force: true}); err != nil {
		log.Errorf("Error deleting workspace: %v", err)
	}
}

// NewTempSSHConfig creates a temporary ssh config file and returns its path and a cleanup func.
func NewTempSSHConfig() (string, func(), error) {
	f, err := os.CreateTemp("", config.BinaryName+"ssh.config")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", nil, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func (cmd *BuildCmd) build(
	ctx context.Context,
	workspaceClient client.WorkspaceClient,
) error {
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	emitJSON := mode == output.ModeJSON

	if err = workspaceClient.Lock(ctx); err != nil {
		return err
	}
	defer workspaceClient.Unlock()

	if err = clientimplementation.StartWait(ctx, workspaceClient, true); err != nil {
		return err
	}

	log.Infof("building devcontainer")
	defer func() {
		log.Debugf("done building devcontainer")
		log.Infof("cleaning up temporary workspace")
	}()

	result, err := clientimplementation.BuildAgentClient(
		ctx,
		clientimplementation.BuildAgentClientOptions{
			WorkspaceClient: workspaceClient,
			CLIOptions:      cmd.CLIOptions,
			AgentCommand:    "build",
		},
	)
	if err != nil {
		if emitJSON {
			_ = devcconfig.WriteErrorJSON(os.Stdout, err.Error())
		}
		return err
	}

	if !emitJSON {
		return nil
	}

	writeBuildResultJSON(result)
	return nil
}

func writeBuildResultJSON(result *devcconfig.Result) {
	workdir := ""
	if result != nil && result.SubstitutionContext != nil {
		workdir = result.SubstitutionContext.ContainerWorkspaceFolder
	}
	_ = devcconfig.WriteResultJSON(os.Stdout, devcconfig.ResultEnvelope{
		ContainerID:           devcconfig.GetContainerID(result),
		RemoteUser:            devcconfig.GetRemoteUser(result),
		RemoteWorkspaceFolder: workdir,
	})
}
