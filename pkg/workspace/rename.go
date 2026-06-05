package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
)

// RenameOptions holds parameters for renaming a workspace.
type RenameOptions struct {
	DevsyConfig *config.Config
	OldName     string
	NewName     string
}

func moveWorkspace(devsyConfig *config.Config, oldName, newName string) error {
	oldDir, err := provider.GetWorkspaceDir(devsyConfig.DefaultContext, oldName)
	if err != nil {
		return fmt.Errorf("get old workspace dir: %w", err)
	}

	newDir, err := provider.GetWorkspaceDir(devsyConfig.DefaultContext, newName)
	if err != nil {
		return fmt.Errorf("get new workspace dir: %w", err)
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename workspace dir: %w", err)
	}

	return nil
}

// stopWorkspaceIfRunning acquires a lock on the workspace, checks its status,
// and stops it if it is not already stopped.
func stopWorkspaceIfRunning(
	ctx context.Context,
	devsyConfig *config.Config,
	wsConfig *provider.Workspace,
) error {
	wsClient, err := Get(ctx, GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{wsConfig.ID},
		Owner:       platform.AllOwnerFilter,
	})
	if err != nil {
		return fmt.Errorf("get workspace client: %w", err)
	}

	if err := wsClient.Lock(ctx); err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	defer wsClient.Unlock()

	status, err := wsClient.Status(ctx, client2.StatusOptions{ContainerStatus: true})
	if err != nil {
		return fmt.Errorf("get workspace status: %w", err)
	}

	if status == client2.StatusStopped || status == client2.StatusNotFound {
		return nil
	}

	log.Infof("stopping workspace %s before rename", wsConfig.ID)
	if err := wsClient.Stop(ctx, client2.StopOptions{}); err != nil {
		return fmt.Errorf("stop workspace before rename: %w", err)
	}

	return nil
}

type pathReplacer struct {
	pairs   [][2]string
	changed bool
}

func newPathReplacer(
	localWorkspaceFolder, oldName, newName string,
) *pathReplacer {
	r := &pathReplacer{}

	if localWorkspaceFolder != "" {
		localParent := strings.TrimSuffix(
			localWorkspaceFolder, filepath.Base(localWorkspaceFolder),
		)
		r.pairs = append(r.pairs, [2]string{
			localParent + oldName,
			localParent + newName,
		})
	}

	return r
}

func (r *pathReplacer) replace(s string) string {
	for _, pair := range r.pairs {
		if strings.Contains(s, pair[0]) {
			s = strings.ReplaceAll(s, pair[0], pair[1])
			r.changed = true
		}
	}
	return s
}

func (r *pathReplacer) applyToMergedConfig(mc *devcontainerconfig.MergedDevContainerConfig) {
	if mc == nil {
		return
	}
	if mc.WorkspaceMount != nil {
		updated := r.replace(*mc.WorkspaceMount)
		mc.WorkspaceMount = &updated
	}
}

// updateWorkspaceResult rewrites workspace_result.json to replace host-side
// references to the old workspace name with the new one. Only LocalWorkspaceFolder
// and WorkspaceMount are updated — ContainerWorkspaceFolder is an in-container
// path that does not change when the host workspace is renamed.
func updateWorkspaceResult(devsyConfig *config.Config, oldName, newName string) {
	context := devsyConfig.DefaultContext
	result, err := provider.LoadWorkspaceResult(context, newName)
	if err != nil || result == nil {
		return
	}

	var localWSFolder string
	if sc := result.SubstitutionContext; sc != nil {
		localWSFolder = sc.LocalWorkspaceFolder
	}

	r := newPathReplacer(localWSFolder, oldName, newName)

	if sc := result.SubstitutionContext; sc != nil {
		sc.LocalWorkspaceFolder = r.replace(sc.LocalWorkspaceFolder)
		sc.WorkspaceMount = r.replace(sc.WorkspaceMount)
	}
	r.applyToMergedConfig(result.MergedConfig)

	if !r.changed {
		return
	}

	ws := &provider.Workspace{ID: newName, Context: context}
	if err := provider.SaveWorkspaceResult(ws, result); err != nil {
		log.Warnf("failed to update workspace result after rename: %v", err)
	}
}

func removeContainerForRename(
	ctx context.Context,
	wsConfig *provider.Workspace,
	lookupRunnerID string,
) error {
	helper := &docker.DockerHelper{
		DockerCommand: ResolveDockerCommand(wsConfig, ""),
	}

	labels := devcontainerconfig.GetIDLabels(lookupRunnerID, nil)
	container, err := helper.FindDevContainer(ctx, labels)
	if err != nil {
		return fmt.Errorf("find container: %w", err)
	}
	if container == nil {
		return nil
	}

	log.Infof("removing stale container %s after workspace rename", container.ID)
	return helper.Remove(ctx, container.ID)
}

// Rename auto-stops the workspace, moves its directory, deletes the stale
// container, and clears the old SSH entry. The container is rebuilt on the
// next `up`.
func Rename(ctx context.Context, opts RenameOptions) error {
	wsConfig, err := provider.LoadWorkspaceConfig(opts.DevsyConfig.DefaultContext, opts.OldName)
	if err != nil {
		return fmt.Errorf("loading workspace config: %w", err)
	}

	if wsConfig.IsPro() {
		return fmt.Errorf(
			"cannot rename pro workspaces; pro workspaces are managed by the platform",
		)
	}

	if err := stopWorkspaceIfRunning(ctx, opts.DevsyConfig, wsConfig); err != nil {
		return err
	}

	lookupRunnerID := devcontainer.GetRunnerIDFromWorkspace(wsConfig)

	if err := moveWorkspace(opts.DevsyConfig, opts.OldName, opts.NewName); err != nil {
		return fmt.Errorf("moving workspace: %w", err)
	}

	wsConfig.ID = opts.NewName
	wsConfig.Context = opts.DevsyConfig.DefaultContext
	if err := provider.SaveWorkspaceConfig(wsConfig); err != nil {
		wsConfig.ID = opts.OldName
		rollbackErr := moveWorkspace(opts.DevsyConfig, opts.NewName, opts.OldName)
		return errors.Join(err, rollbackErr)
	}

	updateWorkspaceResult(opts.DevsyConfig, opts.OldName, opts.NewName)

	if err := removeContainerForRename(ctx, wsConfig, lookupRunnerID); err != nil {
		log.Warnf(
			"renamed workspace but could not remove the stale container (%v); "+
				"run `devsy up %s --recreate` to rebuild it",
			err, opts.NewName,
		)
	}

	_ = devssh.RemoveFromConfig(
		opts.OldName,
		wsConfig.SSHConfigPath,
		wsConfig.SSHConfigIncludePath,
	)

	log.Infof("renamed workspace %s to %s", opts.OldName, opts.NewName)
	return nil
}
