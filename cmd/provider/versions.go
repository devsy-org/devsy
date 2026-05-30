package provider

import (
	"errors"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// VersionsCmd holds the cmd flags.
type VersionsCmd struct {
	*flags.GlobalFlags
}

// NewVersionsCmd creates the cobra command for `provider versions`.
// Note: This is a stub for Task 2.9 registration; Task 2.10 fills in real flags and behavior.
func NewVersionsCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &VersionsCmd{GlobalFlags: f}
	_ = cmd
	return &cobra.Command{
		Use:   "versions [name]",
		Short: "List available upstream versions for a provider",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return errors.New("versions subcommand not yet implemented")
		},
	}
}
