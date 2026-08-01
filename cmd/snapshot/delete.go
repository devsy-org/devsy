package snapshot

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/spf13/cobra"
)

type DeleteCmd struct {
	*flags.GlobalFlags
}

func NewDeleteCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{GlobalFlags: globalFlags}
	deleteCmd := &cobra.Command{
		Use:   "delete <snapshot-ref>",
		Short: "Delete a snapshot manifest from its registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}
	return deleteCmd
}

func (cmd *DeleteCmd) Run(ctx context.Context, ref string) error {
	if err := cmd.validateRef(ref); err != nil {
		return err
	}
	if err := snapshot.DeleteManifest(ctx, ref); err != nil {
		return fmt.Errorf("delete snapshot %s: %w", ref, err)
	}
	log.Infof("deleted snapshot: ref=%s", ref)
	return nil
}

func (cmd *DeleteCmd) validateRef(ref string) error {
	_, err := snapshot.ParseRef(ref)
	if err != nil {
		return fmt.Errorf("invalid snapshot ref %q: %w", ref, err)
	}
	return nil
}
