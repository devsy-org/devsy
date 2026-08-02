package snapshot

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	up "github.com/devsy-org/devsy/cmd/workspace/up"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/spf13/cobra"
)

type RestoreCmd struct {
	*flags.GlobalFlags

	WorkspaceID  string
	ProviderName string
}

func NewRestoreCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &RestoreCmd{GlobalFlags: globalFlags}
	restoreCmd := &cobra.Command{
		Use:   "restore [flags] <snapshot-ref>",
		Short: "Restore or transfer a workspace from a snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			return cmd.Run(cobraCmd.Context(), devsyConfig, args[0])
		},
	}
	cliflags.Add(
		restoreCmd,
		cliflags.String(
			&cmd.WorkspaceID,
			"workspace-id",
			"",
			"Workspace id to restore into (defaults to the snapshot's original workspace id)",
		),
		cliflags.String(
			&cmd.ProviderName,
			"provider",
			"",
			"Provider to restore into (defaults to the current default provider)",
		),
	)
	return restoreCmd
}

// Run reuses the same creation/start path `devsy up` uses
// (pkg/workspace.Resolve plus the agent run), with the workspace's source
// pinned to the snapshot ref and the devcontainer image pinned to the
// snapshot's committed filesystem layer, so the snapshot volume restore
// wired into container setup (cmd/internal/agentcontainer/setup.go) runs
// unchanged.
func (cmd *RestoreCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	snapshotRef string,
) error {
	manifest, err := snapshot.PullManifest(ctx, snapshotRef)
	if err != nil {
		return fmt.Errorf("pull snapshot manifest: %w", err)
	}

	// No devcontainer-hash drift warning here: DevContainerSource is pinned
	// to the snapshot's committed "image:" ref up front, so restore never
	// resolves a devcontainer.json to compare manifest.Annotations[
	// snapshot.AnnotationDevContainerHash] against. Revisit if restore ever
	// gains a mode that resolves a local devcontainer config before pinning
	// the image.
	source, devContainerSource, err := snapshot.RestoreComposition(snapshotRef)
	if err != nil {
		return err
	}

	ws, err := cmd.buildWorkspace(manifest, snapshotRef, devContainerSource)
	if err != nil {
		return err
	}

	runArgs, err := manifest.RunArgs()
	if err != nil {
		return fmt.Errorf("read snapshot run args: %w", err)
	}

	log.Infof("restoring snapshot: ref=%s workspaceId=%s", snapshotRef, ws.ID)

	return up.RunFromOptions(ctx, cmd.GlobalFlags, up.Options{
		Source:             source,
		Name:               ws.ID,
		Provider:           ws.Provider.Name,
		IDE:                "none",
		DevContainerSource: ws.DevContainerSource,
		RunArgs:            runArgs,
	})
}

// devContainerSource is the (already "image:"-prefixed) ref from
// snapshot.RestoreComposition, routing the restored workspace through
// devcontainer.SourceImage (pkg/devcontainer/source.go) to run the committed
// image directly instead of rebuilding it.
func (cmd *RestoreCmd) buildWorkspace(
	manifest *snapshot.Manifest, snapshotRef, devContainerSource string,
) (*provider.Workspace, error) {
	workspaceID := cmd.WorkspaceID
	if workspaceID == "" {
		ref, err := snapshot.ParseRef(snapshotRef)
		if err != nil {
			return nil, fmt.Errorf("parse snapshot ref: %w", err)
		}
		workspaceID = ref.WorkspaceID
	}
	providerName := cmd.ProviderName
	if providerName == "" {
		providerName = manifest.Annotations[snapshot.AnnotationSourceProvider]
	}

	return &provider.Workspace{
		ID: workspaceID,
		Source: provider.WorkspaceSource{
			Snapshot: snapshotRef,
		},
		DevContainerSource: devContainerSource,
		Provider: provider.WorkspaceProviderConfig{
			Name: providerName,
		},
		CreationTimestamp: types.Now(),
		LastUsedTimestamp: types.Now(),
	}, nil
}
