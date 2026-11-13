package flags

import (
	"context"

	"github.com/spf13/cobra"
)

const inspectionAnnotation = "devsy.dependency-inspection"

// MarkDependencyInspection opts only this command into inspection framing.
func MarkDependencyInspection(command *cobra.Command) {
	if command.Annotations == nil {
		command.Annotations = map[string]string{}
	}
	command.Annotations[inspectionAnnotation] = "true"
}

func IsDependencyInspection(command *cobra.Command) bool {
	return command != nil && command.Annotations[inspectionAnnotation] == "true"
}

// InspectionInvocation records argv intent independently of environment-bound flags.
type InspectionInvocation struct {
	JSON            bool
	ProviderPresent bool
	Provider        string
}

type inspectionInvocationKey struct{}

func WithInspectionInvocation(
	ctx context.Context,
	invocation InspectionInvocation,
) context.Context {
	return context.WithValue(ctx, inspectionInvocationKey{}, invocation)
}

func InspectionInvocationFromContext(ctx context.Context) InspectionInvocation {
	if ctx == nil {
		return InspectionInvocation{}
	}
	invocation, _ := ctx.Value(inspectionInvocationKey{}).(InspectionInvocation)
	return invocation
}
