package flags

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/platform/client"
	flag "github.com/spf13/pflag"
)

// GlobalFlags is the flags that contains the global flags.
type GlobalFlags struct {
	*flags.GlobalFlags

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
