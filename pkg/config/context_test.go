package config

import (
	"fmt"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

type testCaseMergeContextOption struct {
	description string
	in          *ContextConfig
	environ     []string
	expected    *ContextConfig
}

func TestCaseMergeContextOption(t *testing.T) {
	testCases := []testCaseMergeContextOption{
		{
			description: "empty config, nothing in env",
			in:          &ContextConfig{},
			environ:     []string{},
			expected:    &ContextConfig{},
		},
		{
			description: "docker injection is false, nothing coming in from env",
			in: &ContextConfig{
				Options: map[string]OptionValue{
					ContextOptionSSHInjectDockerCredentials: {
						Value: "false",
					},
				},
			},
			environ: []string{},
			expected: &ContextConfig{
				Options: map[string]OptionValue{
					ContextOptionSSHInjectDockerCredentials: {
						Value: "false",
					},
				},
			},
		},
		{
			description: "docker injection set by env",
			in: &ContextConfig{
				Options: map[string]OptionValue{},
			},
			environ: []string{
				fmt.Sprintf("%s=%s", ContextOptionSSHInjectDockerCredentials, "true"),
			},
			expected: &ContextConfig{
				Options: map[string]OptionValue{
					ContextOptionSSHInjectDockerCredentials: {
						Value:        "true",
						UserProvided: true,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		MergeContextOptions(tc.in, tc.environ)
		ok := assert.Check(
			t,
			cmp.DeepEqual(tc.expected, tc.in, gocmp.FilterPath(func(p gocmp.Path) bool {
				return p.String() != "Filled"
			}, gocmp.Ignore())),
		)
		if !ok {
			fmt.Println(tc.description)
		}
	}
}

func TestSnapshotRegistryOption_DefaultsEmpty(t *testing.T) {
	c := &Config{Contexts: map[string]*ContextConfig{"default": {}}, DefaultContext: "default"}
	require.Equal(t, "", c.ContextOption(ContextOptionSnapshotRegistry))
}

func TestSnapshotRegistryOption_Registered(t *testing.T) {
	found := false
	for _, opt := range ContextOptions {
		if opt.Name == ContextOptionSnapshotRegistry {
			found = true
		}
	}
	require.True(t, found, "SNAPSHOT_REGISTRY must be a registered context option")
}
