package flags

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestPassiveMarkerDoesNotInherit(t *testing.T) {
	parent := &cobra.Command{Use: "parent", Annotations: map[string]string{"unrelated": "retained"}}
	child := &cobra.Command{Use: "child"}
	parent.AddCommand(child)
	MarkPassive(parent)
	require.True(t, IsPassive(parent))
	require.Equal(t, "retained", parent.Annotations["unrelated"])
	require.False(t, IsPassive(child))
	require.False(t, IsPassive(nil))
	MarkPassive(child)
	require.True(t, IsPassive(child))
}

func TestPassiveInputsRemainCallerOwned(t *testing.T) {
	ctx := context.Background()
	_, ok := PassiveInputsFromContext(ctx)
	require.False(t, ok)
	inputs := PassiveInputs{
		Paths:  config.NewReadOnlyPathManagerForHome(t.TempDir()),
		Config: &config.Config{},
	}
	prepared := WithPassiveInputs(ctx, inputs)
	got, ok := PassiveInputsFromContext(prepared)
	require.True(t, ok)
	require.Same(t, inputs.Paths, got.Paths)
	require.Same(t, inputs.Config, got.Config)
}
