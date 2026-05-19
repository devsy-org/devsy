package cmd

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/selfupdate"
	"github.com/spf13/cobra"
)

// SelfUpdateCmd is a struct that defines a command call for "self-update".
type SelfUpdateCmd struct {
	Version string
	Channel string
	DryRun  bool
}

// NewSelfUpdateCmd creates a new self-update command.
func NewSelfUpdateCmd() *cobra.Command {
	cmd := &SelfUpdateCmd{}
	selfUpdateCmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update the Devsy CLI to the newest version",
		Args:  cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			switch cmd.Channel {
			case "stable", "beta":
				return nil
			default:
				return fmt.Errorf("invalid channel %q: must be 'stable' or 'beta'", cmd.Channel)
			}
		},
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			opts := selfupdate.Options{
				Version:           cmd.Version,
				DryRun:            cmd.DryRun,
				IncludePrerelease: cmd.Channel == "beta",
			}
			if err := selfupdate.Upgrade(ctx, opts); err != nil {
				return fmt.Errorf("unable to update: %w", err)
			}
			return nil
		},
	}

	selfUpdateCmd.Flags().
		StringVar(&cmd.Version, "version", "",
			"The version to update to. Defaults to the latest stable version available")
	selfUpdateCmd.Flags().
		StringVar(&cmd.Channel, "channel", "stable",
			"Release channel: 'stable' for production releases, 'beta' for pre-release versions")
	selfUpdateCmd.Flags().
		BoolVar(&cmd.DryRun, "dry-run", false, "Show which version would be downloaded without actually updating")
	return selfUpdateCmd
}
