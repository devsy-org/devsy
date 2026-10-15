package main

import (
	"github.com/devsy-org/devsy/e2e/framework/dockerwsl"
	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/spf13/cobra"
)

func newRootCommand(deps *runenv.Dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "devsy-ci",
		Short:         "CI helper for provisioning Devsy E2E runtimes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(dockerwsl.NewCommand(deps))
	return root
}
