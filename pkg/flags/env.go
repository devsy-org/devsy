package flags

import (
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	pflag "github.com/spf13/pflag"
)

// EnvName returns the canonical env var name for a flag under the uniform rule:
//
//	config.EnvPrefix + uppercase(flagName) with "-" replaced by "_"
//
// Example: "agent-url" -> "DEVSY_AGENT_URL".
func EnvName(flagName string) string {
	return config.EnvPrefix + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// BindEnv wires the canonical DEVSY_* env var into a cobra flag. Call it
// immediately after the flag's *Var registration. If the env var is set, the
// value is applied to the flag (Changed=true, satisfying MarkFlagRequired) and
// the usage string advertises the env var.
//
// Failure modes are loud by design:
//   - panics if the flag is not registered (programmer bug at startup);
//   - log.Fatalf if the env value is rejected by the flag's type (operator bug;
//     e.g. DEVSY_DEBUG=garbage on a bool flag). This matches the contract of
//     the prior inheritFlagsFromEnvironment loop so misconfigured env vars are
//     never silently ignored.
func BindEnv(fs *pflag.FlagSet, flagName string) {
	f := fs.Lookup(flagName)
	if f == nil {
		panic("flags.BindEnv: flag --" + flagName + " is not registered")
	}
	envName := EnvName(flagName)
	// "[$DEVSY_FOO]" — $VAR convention, bracket avoids clashing with
	// parentheses inside the existing description.
	suffix := " [$" + envName + "]"
	desc := strings.TrimRight(f.Usage, " .")
	if !strings.HasSuffix(desc, suffix) {
		f.Usage = desc + suffix
	}
	value, ok := os.LookupEnv(envName)
	if !ok || value == "" {
		return
	}
	if err := fs.Set(flagName, value); err != nil {
		log.Fatalf("invalid value %q for %s (flag --%s): %v", value, envName, flagName, err)
	}
	f.DefValue = value
}
