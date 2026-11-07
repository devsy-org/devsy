package flags

import (
	"context"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
)

const startupPolicyAnnotation = "devsy.passive-startup"

// MarkPassive opts this command alone into the passive root lifecycle.
// Constructors must mark each leaf separately, including children of marked groups.
func MarkPassive(command *cobra.Command) {
	if command.Annotations == nil {
		command.Annotations = map[string]string{}
	}
	command.Annotations[startupPolicyAnnotation] = config.BoolTrue
}

// IsPassive reports only the command's own static policy, never its ancestors'.
func IsPassive(command *cobra.Command) bool {
	return command != nil && command.Annotations[startupPolicyAnnotation] == config.BoolTrue
}

// PassiveInputs holds caller-owned inputs prepared by the root lifecycle.
// The config remains mutable; the path manager resolves paths without creating roots.
type PassiveInputs struct {
	Paths  config.PathManager
	Config *config.Config
}

type passiveInputsKey struct{}

// WithPassiveInputs attaches the prepared inputs for a passive child command.
func WithPassiveInputs(ctx context.Context, inputs PassiveInputs) context.Context {
	return context.WithValue(ctx, passiveInputsKey{}, inputs)
}

// PassiveInputsFromContext returns the inputs after passive root preparation.
func PassiveInputsFromContext(ctx context.Context) (PassiveInputs, bool) {
	inputs, ok := ctx.Value(passiveInputsKey{}).(PassiveInputs)
	return inputs, ok
}
