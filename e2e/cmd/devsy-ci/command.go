package main

import (
	"github.com/devsy-org/devsy/e2e/framework/dockerwsl"
	"github.com/devsy-org/devsy/e2e/framework/e2erun"
	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/spf13/cobra"
)

// newRootCommand wires every subcommand to the shared dependencies.
func newRootCommand(deps *runenv.Dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "devsy-ci",
		Short:         "CI helper for provisioning and verifying Devsy E2E runtimes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		e2erun.NewCommand(deps, dockerwsl.VerifySetup),
		dockerwsl.NewCommand(deps),
	)
	return root
}
