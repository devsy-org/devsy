package workspace

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListCmdSkipPlatformFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "defaults to false", args: nil, want: false},
		{name: "skip-platform sets value", args: []string{"--" + names.SkipPlatform}, want: true},
		{
			name: "skip-pro alias sets same value",
			args: []string{"--" + names.SkipProAlias},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewListCmd(&flags.GlobalFlags{})
			require.NoError(t, cmd.ParseFlags(tt.args))

			got, err := cmd.Flags().GetBool(names.SkipPlatform)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestListCmdSkipProAliasIsHidden(t *testing.T) {
	cmd := NewListCmd(&flags.GlobalFlags{})

	alias := cmd.Flags().Lookup(names.SkipProAlias)
	require.NotNil(t, alias)
	assert.True(t, alias.Hidden)

	visible := cmd.Flags().Lookup(names.SkipPlatform)
	require.NotNil(t, visible)
	assert.False(t, visible.Hidden)
}
