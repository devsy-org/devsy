package config

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewConfigCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	_ = globalFlags
	return &cobra.Command{
		Use:   "config",
		Short: "Read and apply devcontainer configuration",
	}
}
