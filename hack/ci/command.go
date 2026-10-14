package main

import (
	"github.com/spf13/cobra"
)

// extraCommands lets other files in this package register top-level commands
// (for example docker-wsl) from an init function. The root calls each builder
// with the shared dependencies.
var extraCommands []func(*Dependencies) *cobra.Command

func newRootCommand(deps *Dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "devsy-ci",
		Short:         "CI helper for provisioning and verifying Devsy E2E runtimes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newE2ECommand(deps))
	for _, build := range extraCommands {
		root.AddCommand(build(deps))
	}
	return root
}
