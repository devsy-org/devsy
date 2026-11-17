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

// EnvAnnotation keys the flag's env var name for the help renderer, which shows
// it beside the flag rather than buried in the description.
const EnvAnnotation = "devsy_env"

// BindEnv wires the canonical DEVSY_* env var into a cobra flag. Call it
// immediately after the flag's *Var registration. If the env var is set, the
// value is applied to the flag (Changed=true, satisfying MarkFlagRequired) and
// the env var is recorded under [EnvAnnotation] for the help renderer.
func BindEnv(fs *pflag.FlagSet, flagName string) {
	f := fs.Lookup(flagName)
	if f == nil {
		panic("flags.BindEnv: flag --" + flagName + " is not registered")
	}
	envName := EnvName(flagName)
	if f.Annotations == nil {
		f.Annotations = map[string][]string{}
	}
	f.Annotations[EnvAnnotation] = []string{envName}
	value, ok := os.LookupEnv(envName)
	if !ok || value == "" {
		return
	}
	if err := fs.Set(flagName, value); err != nil {
		log.Fatalf("invalid value for %s (flag --%s)", envName, flagName)
	}
	f.DefValue = value
}
