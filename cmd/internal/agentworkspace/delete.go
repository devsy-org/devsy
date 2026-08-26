package agentworkspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	agentdaemon "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// DeleteCmd holds the cmd flags.
type DeleteCmd struct {
	*flags.GlobalFlags

	Container     bool
	Daemon        bool
	RemoveVolumes bool

	WorkspaceInfo string
}

// NewDeleteCmd creates a new command.
func NewDeleteCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{
		GlobalFlags: flags,
	}
	deleteCmd := &cobra.Command{
		Use:     "delete",
		Aliases: []string{"rm"},
		Short:   "Cleans up a workspace on the remote server",
		Args:    cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(
		deleteCmd,
		cliflags.Bool(
			&cmd.Container,
			names.Container,
			true,
			"If enabled, cleans up the Devsy container",
		),
		cliflags.Bool(&cmd.Daemon, names.Daemon, false, "If enabled, cleans up the Devsy daemon"),
		cliflags.Bool(
			&cmd.RemoveVolumes,
			names.RemoveVolumes,
			false,
			"Remove declared volumes when deleting docker compose workspaces",
		),
		cliflags.String(&cmd.WorkspaceInfo, names.WorkspaceInfo, "", "The workspace info"),
	)
	_ = deleteCmd.MarkFlagRequired(names.WorkspaceInfo)
	return deleteCmd
}

func (cmd *DeleteCmd) Run(ctx context.Context) error {
	shouldExit, workspaceInfo, err := agent.WorkspaceInfo(
		cmd.WorkspaceInfo,
	)
	if err != nil {
		return fmt.Errorf("error parsing workspace info: %w", err)
	} else if shouldExit {
		return nil
	}

	var errs []error
	if cmd.Daemon {
		if err := removeDaemon(workspaceInfo); err != nil {
			errs = append(errs, fmt.Errorf("remove daemon: %w", err))
		}
	}
	if cmd.Container {
		if err := removeContainer(ctx, workspaceInfo, cmd.RemoveVolumes); err != nil {
			errs = append(errs, fmt.Errorf("remove container: %w", err))
		}
	}

	removeWorkspaceFolders(workspaceInfo)

	return errors.Join(errs...)
}

// removeWorkspaceFolders deletes the workspace's config folder and cached
// content folder, logging rather than failing on either error since deletion
// should proceed best-effort once the container/daemon are already gone.
func removeWorkspaceFolders(workspaceInfo *provider2.AgentWorkspaceInfo) {
	if err := forceRemoveAll(workspaceInfo.Origin); err != nil {
		log.Errorf("remove workspace folder: %v", err)
	}
	if err := removeContentFolder(workspaceInfo); err != nil {
		log.Errorf("remove workspace content folder: %v", err)
	}
}

// removeContentFolder deletes the workspace's cached content folder, unless
// it's the user's own local folder mounted directly rather than a devsy copy.
func removeContentFolder(workspaceInfo *provider2.AgentWorkspaceInfo) error {
	if workspaceInfo.ContentFolder == "" ||
		workspaceInfo.ContentFolder == workspaceInfo.Workspace.Source.LocalFolder {
		return nil
	}
	return forceRemoveAll(workspaceInfo.ContentFolder)
}

func removeContainer(
	ctx context.Context,
	workspaceInfo *provider2.AgentWorkspaceInfo,
	removeVolumes bool,
) error {
	log.Debugf("removing Devsy container from server: workspaceId=%s", workspaceInfo.Workspace.ID)
	runner, err := CreateRunner(ctx, workspaceInfo)
	if err != nil {
		return err
	}

	if err := runner.Delete(ctx, devcontainer.DeleteOptions{
		RemoveVolumes: removeVolumes,
	}); err != nil {
		return err
	}
	log.Debug("removed Devsy container from server")

	return nil
}

// forceRemoveAll attempts os.RemoveAll and, on failure, makes all directories
// writable before retrying. Container runtimes (e.g. crun with Podman) can
// leave directories without write permission, causing a standard RemoveAll to
// fail with "permission denied".
func forceRemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}

	// Walk the tree and add owner-write+execute to every directory so
	// entries inside them can be unlinked on the retry.
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		_ = os.Chmod(
			p,
			info.Mode()|0o700,
		) // #nosec G122 -- intentional: fixing perms for deletion, path is not user-controlled
		return nil
	})

	return os.RemoveAll(path)
}

func removeDaemon(workspaceInfo *provider2.AgentWorkspaceInfo) error {
	if len(workspaceInfo.Agent.Exec.Shutdown) == 0 {
		return nil
	}

	log.Debug("removing Devsy daemon from server")
	err := agentdaemon.RemoveDaemon()
	if err != nil {
		return fmt.Errorf("remove daemon: %w", err)
	}
	log.Debug("removed Devsy daemon from server")

	return nil
}
