package cmdinternal

import (
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/spf13/cobra"
)

func NewRuntimeSupervisorCmd() *cobra.Command {
	return &cobra.Command{
		Use: "runtime-supervisor", Hidden: true,
		DisableFlagParsing: true,
		PersistentPreRunE:  func(*cobra.Command, []string) error { return nil },
		Run:                func(_ *cobra.Command, args []string) { supervisor.Main(args) },
	}
}
