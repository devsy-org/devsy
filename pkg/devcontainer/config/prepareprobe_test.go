//go:build !windows

package config

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	patchEnvTestPlainValue = "hello"
	patchEnvTestHome       = "HOME"
	patchEnvTestUser       = "USER"
	patchEnvTestLogname    = "LOGNAME"
	patchEnvTestOkEntry    = "OK=1"
)

func envToMap(t *testing.T, entries []string) map[string]string {
	t.Helper()
	got := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		require.True(t, ok, "output entry %q has no separator", entry)
		_, duplicate := got[name]
		require.False(t, duplicate, "output has more than one entry for %q", name)
		got[name] = value
	}
	return got
}

//nolint:funlen // Table of input vectors.
func TestPatchEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		env      []string
		patches  map[string]string
		expected map[string]string
	}{
		{
			name:     "plain value",
			env:      []string{"PLAIN=" + patchEnvTestPlainValue},
			expected: map[string]string{"PLAIN": patchEnvTestPlainValue},
		},
		{
			name:     "one additional equals sign",
			env:      []string{"ONE_EQ=a=b"},
			expected: map[string]string{"ONE_EQ": "a=b"},
		},
		{
			name:     "A=b=c retains the complete value",
			env:      []string{"A=b=c"},
			expected: map[string]string{"A": "b=c"},
		},
		{
			name:     "repeated equals signs",
			env:      []string{"TWO_EQ=a=b=c"},
			expected: map[string]string{"TWO_EQ": "a=b=c"},
		},
		{
			name:     "commas and equals signs",
			env:      []string{"COMMA_EQ=x=1,y=2"},
			expected: map[string]string{"COMMA_EQ": "x=1,y=2"},
		},
		{
			name:     "trailing equals sign",
			env:      []string{"TRAILING_EQ=z="},
			expected: map[string]string{"TRAILING_EQ": "z="},
		},
		{
			name:     "value begins with equals",
			env:      []string{"LEADING_EQ==a"},
			expected: map[string]string{"LEADING_EQ": "=a"},
		},
		{
			name:     "empty value",
			env:      []string{"EMPTY="},
			expected: map[string]string{"EMPTY": ""},
		},
		{
			name:     "whitespace tab and newline are literal",
			env:      []string{"WS=  a=b\t\nc=d  "},
			expected: map[string]string{"WS": "  a=b\t\nc=d  "},
		},
		{
			name:     "quotes and Unicode are literal",
			env:      []string{"LITERAL=\"a=b\"'\u00e9'"},
			expected: map[string]string{"LITERAL": "\"a=b\"'\u00e9'"},
		},
		{
			name:     "empty inherited slice keeps patches",
			patches:  map[string]string{patchEnvTestHome: "/home/u", patchEnvTestUser: "u"},
			expected: map[string]string{patchEnvTestHome: "/home/u", patchEnvTestUser: "u"},
		},
		{
			name:     "last duplicate inherited entry wins",
			env:      []string{"A=first", "A=last=part"},
			expected: map[string]string{"A": "last=part"},
		},
		{
			name: "patch overrides inherited value with equals sign",
			env: []string{
				patchEnvTestHome + "=/old=dir",
				patchEnvTestUser + "=old",
				patchEnvTestLogname + "=old",
			},
			patches: map[string]string{
				patchEnvTestHome:    "/new=dir",
				patchEnvTestUser:    "user=name",
				patchEnvTestLogname: "login=name",
			},
			expected: map[string]string{
				patchEnvTestHome:    "/new=dir",
				patchEnvTestUser:    "user=name",
				patchEnvTestLogname: "login=name",
			},
		},
		{
			name:     "entry without separator is ignored",
			env:      []string{"BROKEN", patchEnvTestOkEntry},
			expected: map[string]string{"OK": "1"},
		},
		{
			name:     "empty entry is ignored",
			env:      []string{"", patchEnvTestOkEntry},
			expected: map[string]string{"OK": "1"},
		},
		{
			name:     "empty name is ignored",
			env:      []string{"=invalid", patchEnvTestOkEntry},
			expected: map[string]string{"OK": "1"},
		},
		{
			name:     "unrelated key is untouched by patches",
			env:      []string{"KEEP=a=b", patchEnvTestHome + "=/old"},
			patches:  map[string]string{patchEnvTestHome: "/new"},
			expected: map[string]string{"KEEP": "a=b", patchEnvTestHome: "/new"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envBefore := slices.Clone(tt.env)
			patchesBefore := maps.Clone(tt.patches)

			got := patchEnvVars(tt.env, tt.patches)

			assert.Equal(t, tt.expected, envToMap(t, got))
			assert.Equal(t, envBefore, tt.env, "env input mutated")
			assert.Equal(t, patchesBefore, tt.patches, "patches input mutated")
		})
	}
}
