package flags

import (
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

	LogOutput    string
	ResultFormat string
	Verbosity    int
	Quiet        bool
	Debug        bool
}

// SetGlobalFlags applies the global flags.
func SetGlobalFlags(flags *flag.FlagSet) *GlobalFlags {
	globalFlags := &GlobalFlags{}

	flags.StringVar(
		&globalFlags.DevsyHome,
		names.Home,
		"",
		"If defined will override the default devsy home",
	)
	flags.StringVar(
		&globalFlags.ResultFormat,
		names.ResultFormat,
		"auto",
		"The result output format. Can be json, plain, or auto (auto picks plain on a TTY and json when piped)",
	)
	flags.StringVar(
		&globalFlags.LogOutput,
		names.LogOutput,
		"text",
		"The log format to use. Can be text, json, or logfmt",
	)
	flags.StringVar(&globalFlags.LogOutput, names.LogFormat, "text", "Alias for --log-output")
	_ = flags.MarkHidden(names.LogFormat)
	flags.StringVar(&globalFlags.Context, names.Context, "", "The context to use")
	flags.StringVar(
		&globalFlags.Provider,
		names.Provider,
		"",
		"The provider to use. Needs to be configured for the selected context",
	)
	flags.CountVarP(
		&globalFlags.Verbosity,
		names.Verbose,
		"v",
		"Increase log verbosity (-v=info, -vv=debug, -vvv=trace)",
	)
	flags.BoolVarP(
		&globalFlags.Quiet,
		names.Quiet,
		"q",
		false,
		"Suppress all log output except fatal errors",
	)
	flags.BoolVar(
		&globalFlags.Debug,
		names.Debug,
		false,
		"Enable debug logging (equivalent to -vv)",
	)

	flags.Var(&globalFlags.Owner, names.Owner, "Show pro workspaces for owner")
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

	BindEnv(flags, names.Home)
	BindEnv(flags, names.Context)
	BindEnv(flags, names.Provider)
	BindEnv(flags, names.Debug)

	return globalFlags
}
