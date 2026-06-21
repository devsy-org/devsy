// release_artifacts: subcommands for inspecting CLI binaries staged by the
// release pipeline. See cmdInventory, cmdStage, and cmdVerify.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:  "release_artifacts",
		Usage: "release-pipeline tools for staging and verifying CLI binaries",
		Commands: []*cli.Command{
			inventoryCmd(),
			populateDistCmd(),
			stageCmd(),
			verifyCmd(),
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
