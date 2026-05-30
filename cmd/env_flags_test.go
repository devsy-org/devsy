package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envDevsyHost    = "DEVSY_HOST"
	envDevsyProject = "DEVSY_PROJECT"
)

// TestOptInEnvFlags_AppliesEnvValueToFlag verifies that for each opt-in flag,
// setting the corresponding DEVSY_* env var pushes the value into the bound
// pflag at registration time, satisfying cobra's required-flag check.
func TestOptInEnvFlags_AppliesEnvValueToFlag(t *testing.T) {
	cases := []struct {
		cmdPath    string
		flagName   string
		envName    string
		want       string
		persistent bool
	}{
		{flagName: "home", envName: "DEVSY_HOME", want: "/tmp/h", persistent: true},
		{flagName: "context", envName: "DEVSY_CONTEXT", want: "ctx-a", persistent: true},
		{flagName: "provider", envName: "DEVSY_PROVIDER", want: "docker", persistent: true},
		{flagName: "debug", envName: "DEVSY_DEBUG", want: "true", persistent: true},
		{
			cmdPath:  "pro workspace list",
			flagName: "host",
			envName:  envDevsyHost,
			want:     "pro.example.com",
		},
		{cmdPath: "pro cluster list", flagName: "project", envName: envDevsyProject, want: "demo"},
		{
			cmdPath:  "agent container setup",
			flagName: "access-key",
			envName:  "DEVSY_ACCESS_KEY",
			want:     "secret",
		},
		{
			cmdPath:  "agent container setup",
			flagName: "platform-host",
			envName:  "DEVSY_PLATFORM_HOST",
			want:     "p.example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.envName, func(t *testing.T) {
			t.Setenv(tc.envName, tc.want)
			rootCmd, _ := BuildRoot()
			cmd := resolveCommand(t, rootCmd, tc.cmdPath)
			fs := cmd.Flags()
			if tc.persistent {
				fs = cmd.PersistentFlags()
			}
			f := fs.Lookup(tc.flagName)
			require.NotNil(t, f, "flag --%s not found on %q", tc.flagName, tc.cmdPath)
			assert.Equal(t, tc.want, f.Value.String())
			assert.True(t, f.Changed, "Changed must be true so MarkFlagRequired passes")
			assert.Contains(t, f.Usage, tc.envName, "usage should advertise env var")
		})
	}
}

func TestOptInEnvFlags_NoEnvLeavesDefault(t *testing.T) {
	for _, name := range []string{"DEVSY_HOME", "DEVSY_CONTEXT", "DEVSY_PROVIDER", envDevsyHost, envDevsyProject} {
		t.Setenv(name, "")
	}
	rootCmd, _ := BuildRoot()
	f := rootCmd.PersistentFlags().Lookup("context")
	require.NotNil(t, f)
	assert.False(t, f.Changed)
	assert.Equal(t, "", f.Value.String())
}

// TestOptInEnvFlags_EmptyEnvIsNoOp guards the "value == \"\"" branch in
// BindEnv: setting DEVSY_HOST="" must NOT mark the flag as Changed (otherwise
// MarkFlagRequired would be satisfied by an empty value).
func TestOptInEnvFlags_EmptyEnvIsNoOp(t *testing.T) {
	t.Setenv(envDevsyHost, "")
	rootCmd, _ := BuildRoot()
	cmd := resolveCommand(t, rootCmd, "pro workspace list")
	f := cmd.Flags().Lookup("host")
	require.NotNil(t, f)
	assert.False(t, f.Changed, "empty env value must not mark flag as Changed")
}

// TestOptInEnvFlags_CLIOverridesEnv proves that an explicit CLI flag value
// wins over a value supplied via DEVSY_*. This is the standard precedence
// users expect: flag > env > default.
func TestOptInEnvFlags_CLIOverridesEnv(t *testing.T) {
	t.Setenv(envDevsyHost, "env-value")
	rootCmd, _ := BuildRoot()
	// Replace the leaf command's RunE so Execute() doesn't try to actually
	// talk to a pro instance; we only care that flag resolution put cli-value
	// in cmd.Host.
	leaf := resolveCommand(t, rootCmd, "pro workspace list")
	var seen string
	leaf.RunE = func(c *cobra.Command, _ []string) error {
		seen, _ = c.Flags().GetString("host")
		return nil
	}
	rootCmd.SetArgs([]string{"pro", "workspace", "list", "--host", "cli-value"})
	require.NoError(t, rootCmd.Execute())
	assert.Equal(t, "cli-value", seen, "CLI flag must override env value")
}

// TestOptInEnvFlags_EnvSatisfiesRequired drives cobra's full parse +
// ValidateRequiredFlags pipeline to prove DEVSY_HOST satisfies
// MarkFlagRequired("host") at execution time, not just in static inspection.
func TestOptInEnvFlags_EnvSatisfiesRequired(t *testing.T) {
	t.Setenv(envDevsyHost, "from-env.example.com")
	rootCmd, _ := BuildRoot()
	leaf := resolveCommand(t, rootCmd, "pro workspace list")
	var seen string
	leaf.RunE = func(c *cobra.Command, _ []string) error {
		seen, _ = c.Flags().GetString("host")
		return nil
	}
	rootCmd.SetArgs([]string{"pro", "workspace", "list"})
	require.NoError(t, rootCmd.Execute(), "required --host must be satisfied by env")
	assert.Equal(t, "from-env.example.com", seen)
}

// TestOptInEnvFlags_ProSubcommandSweep asserts that DEVSY_HOST applies to
// every pro subcommand that exposes --host (and similarly for --project).
// This catches the regression where a future addition to cmd/pro/ forgets
// the inline BindEnv call.
func TestOptInEnvFlags_ProSubcommandSweep(t *testing.T) {
	for _, env := range []string{envDevsyHost, envDevsyProject} {
		t.Setenv(env, "sweep-"+strings.ToLower(env))
	}
	rootCmd, _ := BuildRoot()
	proCmd := resolveCommand(t, rootCmd, "pro")

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, flagName := range []string{"host", "project"} {
			if f := c.Flags().Lookup(flagName); f != nil {
				assert.True(t, f.Changed,
					"%s: --%s should be Changed from DEVSY_%s",
					c.CommandPath(), flagName, strings.ToUpper(flagName),
				)
			}
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(proCmd)
}

func resolveCommand(t *testing.T, rootCmd *cobra.Command, path string) *cobra.Command {
	t.Helper()
	if path == "" {
		return rootCmd
	}
	cur := rootCmd
	for seg := range strings.SplitSeq(path, " ") {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			use := c.Use
			if i := strings.IndexByte(use, ' '); i >= 0 {
				use = use[:i]
			}
			if use == seg {
				next = c
				break
			}
		}
		require.NotNil(t, next, "segment %q not found under %q", seg, cur.Use)
		cur = next
	}
	return cur
}
