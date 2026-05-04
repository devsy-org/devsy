package workspace

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// BuildCmd holds the cmd flags.
type BuildCmd struct {
	*flags.GlobalFlags

	WorkspaceInfo string
}

// NewBuildCmd creates a new command.
func NewBuildCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &BuildCmd{
		GlobalFlags: flags,
	}
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Builds a devcontainer",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	buildCmd.Flags().StringVar(&cmd.WorkspaceInfo, "workspace-info", "", "The workspace info")
	_ = buildCmd.MarkFlagRequired("workspace-info")
	return buildCmd
}

// Run runs the command logic.
func (cmd *BuildCmd) Run(ctx context.Context) error {
	// write workspace info
	shouldExit, workspaceInfo, err := agent.WriteWorkspaceInfoAndDeleteOld(
		cmd.WorkspaceInfo,
		func(workspaceInfo *provider2.AgentWorkspaceInfo) error {
			return deleteWorkspace(ctx, workspaceInfo)
		},
	)
	if err != nil {
		return err
	} else if shouldExit {
		return nil
	}

	// make sure daemon does shut us down while we are doing things
	agent.CreateWorkspaceBusyFile(workspaceInfo.Origin)
	defer agent.DeleteWorkspaceBusyFile(workspaceInfo.Origin)

	// initialize the workspace
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, credentialsDir, err := initWorkspace(initWorkspaceParams{
		ctx:                 cancelCtx,
		workspaceInfo:       workspaceInfo,
		debug:               cmd.Debug,
		shouldInstallDaemon: false,
	})
	if err != nil {
		return err
	} else if credentialsDir != "" {
		defer func() {
			_ = os.RemoveAll(credentialsDir)
		}()
	}

	runner, err := CreateRunner(workspaceInfo)
	if err != nil {
		return err
	}

	// if there is no platform specified, we use empty to let
	// the builder find out itself.
	platforms := workspaceInfo.CLIOptions.Platforms
	if len(platforms) == 0 {
		platforms = []string{""}
	}

	// build and push images
	for _, platform := range platforms {
		// build the image
		imageName, err := runner.Build(ctx, provider2.BuildOptions{
			CLIOptions:    workspaceInfo.CLIOptions,
			RegistryCache: workspaceInfo.RegistryCache,
			Platform:      platform,
			ExportCache:   true,
		})
		if err != nil {
			log.Errorf("Error building image: %v", err)
			return fmt.Errorf("build: %w", err)
		}

		if workspaceInfo.CLIOptions.SkipPush {
			log.Infof("done building image %s", imageName)
		} else {
			log.Infof("done building and pushing image %s", imageName)
		}
	}

	return nil
}

func deleteWorkspace(
	ctx context.Context,
	workspaceInfo *provider2.AgentWorkspaceInfo,
) error {
	err := removeContainer(ctx, workspaceInfo, false)
	if err != nil {
		log.Errorf("Removing container: %v", err)
	}

	_ = os.RemoveAll(workspaceInfo.Origin)
	return nil
}
