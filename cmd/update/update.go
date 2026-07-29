package update

import (
	"fmt"

	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/selfupdate"
	"github.com/spf13/cobra"
)

const (
	channelStable = "stable"
	channelBeta   = "beta"
)

// UpdateCmd is a struct that defines a command call for "update".
type UpdateCmd struct {
	Version string
	Channel string
	DryRun  bool
}

// NewUpdateCmd creates a new update command.
func NewUpdateCmd() *cobra.Command {
	cmd := &UpdateCmd{}
	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Update the CLI to the newest version",
		Args:  cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			switch cmd.Channel {
			case channelStable, channelBeta:
				return nil
			default:
				return fmt.Errorf(
					"invalid channel %q: must be %q or %q",
					cmd.Channel,
					channelStable,
					channelBeta,
				)
			}
		},
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			opts := selfupdate.Options{
				Version:           cmd.Version,
				DryRun:            cmd.DryRun,
				IncludePrerelease: cmd.Channel == channelBeta,
			}
			if err := selfupdate.Upgrade(ctx, opts); err != nil {
				return fmt.Errorf("unable to update: %w", err)
			}
			return nil
		},
	}

	cliflags.Add(
		updateCmd,
		cliflags.String(
			&cmd.Version,
			names.Version,
			"",
			"The version to update to. Defaults to the latest stable version available",
		),
		cliflags.String(
			&cmd.Channel,
			names.Channel,
			channelStable,
			"Release channel: 'stable' for production releases, 'beta' for pre-release versions",
		),
		cliflags.Bool(
			&cmd.DryRun,
			names.DryRun,
			false,
			"Show which version would be downloaded without actually updating",
		),
	)
	return updateCmd
}
