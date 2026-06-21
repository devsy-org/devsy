// release_artifacts: subcommands for inspecting CLI binaries staged by the
// release pipeline.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "release_artifacts",
		Short:         "release-pipeline tools for staging and verifying CLI binaries",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		inventoryCmd(),
		populateDistCmd(),
		stageCmd(),
		verifyCmd(),
	)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
