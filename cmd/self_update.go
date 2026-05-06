package cmd

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/selfupdate"
	"github.com/spf13/cobra"
)

// SelfUpdateCmd is a struct that defines a command call for "self-update".
type SelfUpdateCmd struct {
	Version string
	DryRun  bool
}

// NewSelfUpdateCmd creates a new self-update command.
func NewSelfUpdateCmd() *cobra.Command {
	cmd := &SelfUpdateCmd{}
	selfUpdateCmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update the Devsy CLI to the newest version",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			if err := selfupdate.Upgrade(ctx, cmd.Version, cmd.DryRun); err != nil {
				return fmt.Errorf("unable to update: %w", err)
			}
			return nil
		},
	}

	selfUpdateCmd.Flags().
		StringVar(&cmd.Version, "version", "",
			"The version to update to. Defaults to the latest stable version available")
	selfUpdateCmd.Flags().
		BoolVar(&cmd.DryRun, "dry-run", false, "Show which version would be downloaded without actually updating")
	return selfUpdateCmd
}
