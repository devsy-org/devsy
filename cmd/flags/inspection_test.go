package flags

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestInspectionPolicyAndContext(t *testing.T) {
	group := &cobra.Command{Use: "dependency"}
	leaf := &cobra.Command{Use: "inspect"}
	group.AddCommand(leaf)
	MarkDependencyInspection(group)
	require.True(t, IsDependencyInspection(group))
	require.False(t, IsDependencyInspection(leaf))
	require.False(t, IsDependencyInspection(nil))
	require.Empty(t, InspectionInvocationFromContext(context.Background()))
	invocation := InspectionInvocation{JSON: true, ProviderPresent: true, Provider: "renamed"}
	ctx := WithInspectionInvocation(context.Background(), invocation)
	require.Equal(t, invocation, InspectionInvocationFromContext(ctx))
	MarkDependencyInspection(leaf)
	require.True(t, IsDependencyInspection(leaf))
}
