package provider

import (
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
)

func (cmd *InitCmd) registerFlags(initCmd *cobra.Command) {
	cmd.registerOptionFlags(initCmd)
	cmd.registerTestingFlags(initCmd)
}

func (cmd *InitCmd) registerOptionFlags(initCmd *cobra.Command) {
	cliflags.Add(initCmd,
		cliflags.Bool(&cmd.Reset, names.Reset, false,
			"Discard previously stored option answers and re-prompt from scratch"),
		cliflags.Bool(&cmd.SingleMachine, names.SingleMachine, false,
			"Use a single machine for all workspaces"),
		cliflags.StringArray(&cmd.Options, names.Option, nil,
			"Provider option in the form KEY=VALUE").Shorthand("o"),
	)
}

func (cmd *InitCmd) registerTestingFlags(initCmd *cobra.Command) {
	cliflags.Add(initCmd,
		cliflags.Bool(&cmd.SkipInit, names.SkipInit, false,
			"Skip provider init (testing only)").Hidden(),
	)
}
