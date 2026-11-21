package flags

import (
	pkgflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/platform"
	flag "github.com/spf13/pflag"
)

type GlobalFlags struct {
	Context   string
	Provider  string
	AgentDir  string
	DevsyHome string
	UID       string
	Owner     platform.OwnerFilter

	LogFormat    string
	LogLevel     string
	ResultFormat string
	NoStatus     bool
}

// SetGlobalFlags applies the global flags.
func SetGlobalFlags(flags *flag.FlagSet) *GlobalFlags {
	globalFlags := &GlobalFlags{}

	registerCoreFlags(flags, globalFlags)
	registerOutputFlags(flags, globalFlags)
	registerHiddenFlags(flags, globalFlags)
	bindGlobalEnvVars(flags)

	return globalFlags
}

func registerCoreFlags(flags *flag.FlagSet, globalFlags *GlobalFlags) {
	flags.StringVar(
		&globalFlags.DevsyHome,
		names.Home,
		"",
		"If defined will override the default devsy home",
	)
	flags.StringVar(
		&globalFlags.LogLevel,
		names.LogLevel,
		"",
		"The log level override. Can be error, warn, info, debug, or trace",
	)
	flags.StringVar(&globalFlags.Context, names.Context, "", "The context to use")
	flags.StringVar(
		&globalFlags.Provider,
		names.Provider,
		"",
		"The provider to use. Needs to be configured for the selected context",
	)
}

func registerOutputFlags(flags *flag.FlagSet, globalFlags *GlobalFlags) {
	flags.BoolVar(
		&globalFlags.NoStatus, names.NoStatus, false,
		"Suppress live CLI progress messages; preserve results, errors, and recorded events",
	)
	flags.StringVar(
		&globalFlags.ResultFormat,
		names.ResultFormat,
		"auto",
		"The result output format. Can be auto, text, plain, or json",
	)
	flags.StringVar(
		&globalFlags.LogFormat, names.LogFormat, "auto",
		"The log format. Can be auto, text, plain, json, or logfmt",
	)
}

func registerHiddenFlags(flags *flag.FlagSet, globalFlags *GlobalFlags) {
	flags.Var(&globalFlags.Owner, names.Owner, "Show platform workspaces for owner")
	_ = flags.MarkHidden(names.Owner)
	flags.StringVar(&globalFlags.UID, names.UID, "", "Set UID for workspace")
	_ = flags.MarkHidden(names.UID)
	flags.StringVar(
		&globalFlags.AgentDir,
		names.AgentDir,
		"",
		"The data folder where agent data is stored.",
	)
	_ = flags.MarkHidden(names.AgentDir)
}

func bindGlobalEnvVars(flags *flag.FlagSet) {
	pkgflags.BindEnv(flags, names.Home)
	pkgflags.BindEnv(flags, names.Context)
	pkgflags.BindEnv(flags, names.Provider)
	pkgflags.BindEnv(flags, names.LogLevel)
	pkgflags.BindEnv(flags, names.LogFormat)
	pkgflags.BindEnv(flags, names.ResultFormat)
	pkgflags.BindEnv(flags, names.NoStatus)
}
