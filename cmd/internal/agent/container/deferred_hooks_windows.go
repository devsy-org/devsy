//go:build windows

package container

import (
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewDeferredHooksCmd(_ *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "deferred-hooks",
		Short: "Runs deferred lifecycle hooks (phases after waitFor)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("Windows Containers are not supported")
		},
	}
}
