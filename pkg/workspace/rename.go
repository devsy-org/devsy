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
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
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
	containerWorkspaceFolder, localWorkspaceFolder, oldName, newName string,
) *pathReplacer {
	r := &pathReplacer{}

	if containerWorkspaceFolder != "" {
		containerParent := strings.TrimSuffix(
			containerWorkspaceFolder, filepath.Base(containerWorkspaceFolder),
		)
		r.pairs = append(r.pairs, [2]string{
			containerParent + oldName,
			containerParent + newName,
		})
	}

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
	mc.WorkspaceFolder = r.replace(mc.WorkspaceFolder)
	if mc.WorkspaceMount != nil {
		updated := r.replace(*mc.WorkspaceMount)
		mc.WorkspaceMount = &updated
	}
}

// updateWorkspaceResult rewrites workspace_result.json to replace references
// to the old workspace name with the new one. This ensures that cached paths
// like ContainerWorkspaceFolder, LocalWorkspaceFolder, and WorkspaceMount
// stay valid after rename.
func updateWorkspaceResult(devsyConfig *config.Config, oldName, newName string) {
	context := devsyConfig.DefaultContext
	result, err := provider.LoadWorkspaceResult(context, newName)
	if err != nil || result == nil {
		return
	}

	var containerWSFolder, localWSFolder string
	if sc := result.SubstitutionContext; sc != nil {
		containerWSFolder = sc.ContainerWorkspaceFolder
		localWSFolder = sc.LocalWorkspaceFolder
	}

	r := newPathReplacer(containerWSFolder, localWSFolder, oldName, newName)

	if sc := result.SubstitutionContext; sc != nil {
		sc.ContainerWorkspaceFolder = r.replace(sc.ContainerWorkspaceFolder)
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

// Rename performs the workspace rename: auto-stops if running, moves the
// workspace directory, updates the config ID, and removes the old SSH config
// entry. If any step after the directory move fails, the entire operation is
// rolled back.
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

	_ = devssh.RemoveFromConfig(
		opts.OldName,
		wsConfig.SSHConfigPath,
		wsConfig.SSHConfigIncludePath,
	)

	log.Infof("renamed workspace %s to %s", opts.OldName, opts.NewName)
	return nil
}
