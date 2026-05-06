package flags

import (
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	flag "github.com/spf13/pflag"
)

const OutputFormatJSON = "json"

type GlobalFlags struct {
	Context   string
	Provider  string
	AgentDir  string
	DevsyHome string
	UID       string
	Owner     platform.OwnerFilter

	LogOutput    string
	OutputFormat string
	Verbosity    int
	Quiet        bool
	Debug        bool
}

// SetGlobalFlags applies the global flags.
func SetGlobalFlags(flags *flag.FlagSet) *GlobalFlags {
	globalFlags := &GlobalFlags{}

	flags.StringVar(
		&globalFlags.DevsyHome,
		config.BinaryName+"-home",
		"",
		"If defined will override the default devsy home",
	)
	flags.StringVar(
		&globalFlags.LogOutput,
		"log-output",
		"text",
		"The log format to use. Can be text, json, or logfmt",
	)
	flags.StringVar(&globalFlags.LogOutput, "log-format", "text", "Alias for --log-output")
	_ = flags.MarkHidden("log-format")
	flags.StringVar(&globalFlags.Context, "context", "", "The context to use")
	flags.StringVar(
		&globalFlags.Provider,
		"provider",
		"",
		"The provider to use. Needs to be configured for the selected context",
	)
	flags.CountVarP(
		&globalFlags.Verbosity,
		"verbose",
		"v",
		"Increase log verbosity (-v=info, -vv=debug, -vvv=trace)",
	)
	flags.BoolVarP(
		&globalFlags.Quiet,
		"quiet",
		"q",
		false,
		"Suppress all log output except fatal errors",
	)
	flags.BoolVar(&globalFlags.Debug, "debug", false, "Enable debug logging (equivalent to -vv)")
	flags.StringVar(
		&globalFlags.OutputFormat,
		"output-format",
		"",
		"Machine-readable output format for command results (json)",
	)

	flags.Var(&globalFlags.Owner, "owner", "Show pro workspaces for owner")
	_ = flags.MarkHidden("owner")
	flags.StringVar(&globalFlags.UID, "uid", "", "Set UID for workspace")
	_ = flags.MarkHidden("uid")
	flags.StringVar(
		&globalFlags.AgentDir,
		"agent-dir",
		"",
		"The data folder where agent data is stored.",
	)
	_ = flags.MarkHidden("agent-dir")
	return globalFlags
}
