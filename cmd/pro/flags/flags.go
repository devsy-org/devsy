package flags

import (
	rootflags "github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/platform/client"
	flag "github.com/spf13/pflag"
)

// BindEnv re-exports rootflags.BindEnv so pro subcommands can wire env vars
// through their existing local `flags` import alias.
func BindEnv(fs *flag.FlagSet, flagName string) {
	rootflags.BindEnv(fs, flagName)
}

// GlobalFlags is the flags that contains the global flags.
type GlobalFlags struct {
	*rootflags.GlobalFlags

	Config string
}

// SetGlobalFlags applies the global flags.
func SetGlobalFlags(flags *flag.FlagSet) *GlobalFlags {
	globalFlags := &GlobalFlags{}

	flags.StringVar(
		&globalFlags.Config,
		"config",
		client.DefaultCacheConfig,
		"The config to use (will be created if it does not exist)",
	)

	return globalFlags
}
