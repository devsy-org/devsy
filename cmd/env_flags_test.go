package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	pkgflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envDevsyHost        = "DEVSY_HOST"
	envDevsyProject     = "DEVSY_PROJECT"
	cmdWorkspace        = "workspace"
	cmdProvider         = "provider"
	cmdList             = "list"
	testVersionFlag     = "--version"
	testAgentCommand    = "agent"
	testLogLevelEnv     = "DEVSY_LOG_LEVEL"
	testLogFormatEnv    = "DEVSY_LOG_FORMAT"
	testResultFormatEnv = "DEVSY_RESULT_FORMAT"
	testRootCommand     = "root"
)

const (
	noStatusSelector = "-test.run=^TestInvalidNoStatusEnvironmentStopsBeforeCommand$"
	loggingSelector  = "-test.run=^TestInvalidLoggingEnvironmentDoesNotExposeValue$"
)

func TestInvalidNoStatusEnvironmentStopsBeforeCommand(t *testing.T) {
	if os.Getenv("DEVSY_INVALID_NO_STATUS_CHILD") == "1" {
		os.Args = []string{devsy, testVersionFlag}
		_ = run()
		_ = os.WriteFile(os.Getenv("DEVSY_INVALID_NO_STATUS_MARKER"), []byte("ran"), 0o600)
		return
	}

	const secret = "invalid-bool-secret-9471"
	for _, registered := range []bool{false, true} {
		name := "unregistered"
		if registered {
			name = "registered"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "command-ran")
			metrics := filepath.Join(dir, "metrics.jsonl")
			t.Setenv("DEVSY_INVALID_NO_STATUS_CHILD", "1")
			t.Setenv("DEVSY_INVALID_NO_STATUS_MARKER", marker)
			t.Setenv(config.EnvHome, dir)
			t.Setenv("DEVSY_NO_STATUS", secret)
			t.Setenv("DEVSY_TEST_SECRET", "")
			if registered {
				t.Setenv("DEVSY_TEST_SECRET", secret)
			}
			t.Setenv(testLogFormatEnv, logOutputJSON)
			t.Setenv("DEVSY_METRICS_FILE", metrics)
			child := exec.Command(os.Args[0], noStatusSelector)
			var stdout, stderr bytes.Buffer
			child.Stdout = &stdout
			child.Stderr = &stderr
			err := child.Run()
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			require.NotZero(t, exitErr.ExitCode())
			var record map[string]any
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &record))
			assert.Equal(t, "fatal", record["level"])
			assert.Contains(t, record["msg"], "DEVSY_NO_STATUS")
			assert.NotContains(t, stdout.String(), secret)
			assert.NotContains(t, stderr.String(), secret)
			_, err = os.Stat(marker)
			require.True(t, os.IsNotExist(err))
			_, err = os.Stat(metrics)
			require.True(t, os.IsNotExist(err))
		})
	}
}

func TestInvalidLoggingEnvironmentDoesNotExposeValue(t *testing.T) {
	if mode := os.Getenv("DEVSY_TEST_INVALID_LOG_CHILD"); mode != "" {
		os.Args = []string{devsy, "context", cmdList}
		if mode == testAgentCommand {
			os.Args = []string{devsy, "internal", testAgentCommand, "git-credentials"}
		}
		os.Exit(run())
	}
	const canary = "synthetic-invalid-logging-value-81492"
	for _, tc := range []struct {
		name    string
		mode    string
		env     string
		message string
		choice  string
	}{
		{"root level", testRootCommand, testLogLevelEnv, "invalid log level", "trace"},
		{"root format", testRootCommand, testLogFormatEnv, "invalid log format", logOutputLogfmt},
		{"root result format", testRootCommand, testResultFormatEnv, "unexpected output format", "plain"},
		{"agent level", testAgentCommand, testLogLevelEnv, "invalid log level", "trace"},
		{"agent format", testAgentCommand, testLogFormatEnv, "invalid log format", logOutputLogfmt},
		{"agent result format", testAgentCommand, testResultFormatEnv, "unexpected output format", "plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEVSY_TEST_INVALID_LOG_CHILD", tc.mode)
			t.Setenv(config.EnvHome, t.TempDir())
			t.Setenv(testLogLevelEnv, "")
			t.Setenv(testLogFormatEnv, "")
			t.Setenv(testResultFormatEnv, "")
			t.Setenv("DEVSY_NO_STATUS", "")
			t.Setenv("DEVSY_METRICS_FILE", "")
			t.Setenv(tc.env, canary)
			child := exec.Command(os.Args[0], loggingSelector)
			var stdout, stderr bytes.Buffer
			child.Stdout = &stdout
			child.Stderr = &stderr
			err := child.Run()
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			require.NotZero(t, exitErr.ExitCode())
			assert.Contains(t, stderr.String(), tc.message)
			assert.Contains(t, stderr.String(), tc.choice)
			assert.NotContains(t, stderr.String(), canary)
			assert.Empty(t, stdout.String())
		})
	}
}

// TestOptInEnvFlags_AppliesEnvValueToFlag verifies that for each opt-in flag,
// setting the corresponding DEVSY_* env var pushes the value into the bound
// pflag at registration time, satisfying cobra's required-flag check.
func TestOptInEnvFlags_AppliesEnvValueToFlag(t *testing.T) {
	t.Setenv(config.EnvPlatformEnabled, "true")
	cases := []struct {
		cmdPath    string
		flagName   string
		envName    string
		want       string
		persistent bool
	}{
		{flagName: "home", envName: "DEVSY_HOME", want: "/tmp/h", persistent: true},
		{flagName: "context", envName: "DEVSY_CONTEXT", want: "ctx-a", persistent: true},
		{flagName: cmdProvider, envName: "DEVSY_PROVIDER", want: "docker", persistent: true},
		{flagName: "log-level", envName: testLogLevelEnv, want: "debug", persistent: true},
		{flagName: "log-format", envName: testLogFormatEnv, want: logOutputJSON, persistent: true},
		{flagName: "result-format", envName: testResultFormatEnv, want: "text", persistent: true},
		{flagName: "no-status", envName: "DEVSY_NO_STATUS", want: "true", persistent: true},
		{
			cmdPath:  "platform workspace list",
			flagName: "host",
			envName:  envDevsyHost,
			want:     "platform.example.com",
		},
		{
			cmdPath:  "platform cluster list",
			flagName: "project",
			envName:  envDevsyProject,
			want:     "demo",
		},
		{
			cmdPath:  "internal agent container setup",
			flagName: "access-key",
			envName:  "DEVSY_ACCESS_KEY",
			want:     "secret",
		},
		{
			cmdPath:  "internal agent container setup",
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
			assert.Equal(
				t,
				[]string{tc.envName},
				f.Annotations[pkgflags.EnvAnnotation],
				"env var should be annotated for the help renderer",
			)
		})
	}
}

func TestOptInEnvFlags_NoEnvLeavesDefault(t *testing.T) {
	for _, name := range []string{"DEVSY_HOME", "DEVSY_CONTEXT", "DEVSY_PROVIDER", envDevsyHost, envDevsyProject} {
		t.Setenv(name, "")
	}
	rootCmd, _ := BuildRoot()
	f := rootCmd.PersistentFlags().Lookup(names.Context)
	require.NotNil(t, f)
	assert.False(t, f.Changed)
	assert.Equal(t, "", f.Value.String())
}

// TestOptInEnvFlags_EmptyEnvIsNoOp guards the "value == \"\"" branch in
// BindEnv: setting DEVSY_HOST="" must NOT mark the flag as Changed (otherwise
// MarkFlagRequired would be satisfied by an empty value).
func TestOptInEnvFlags_EmptyEnvIsNoOp(t *testing.T) {
	t.Setenv(config.EnvPlatformEnabled, "true")
	t.Setenv(envDevsyHost, "")
	rootCmd, _ := BuildRoot()
	cmd := resolveCommand(t, rootCmd, "platform workspace list")
	f := cmd.Flags().Lookup(names.Host)
	require.NotNil(t, f)
	assert.False(t, f.Changed, "empty env value must not mark flag as Changed")
}

// TestOptInEnvFlags_CLIOverridesEnv proves that an explicit CLI flag value
// wins over a value supplied via DEVSY_*. This is the standard precedence
// users expect: flag > env > default.
func TestOptInEnvFlags_CLIOverridesEnv(t *testing.T) {
	t.Setenv(config.EnvPlatformEnabled, "true")
	t.Setenv(envDevsyHost, "env-value")
	rootCmd, _ := BuildRoot()
	// Replace the leaf command's RunE so Execute() doesn't try to actually
	// talk to a platform instance; we only care that flag resolution put cli-value
	// in cmd.Host.
	leaf := resolveCommand(t, rootCmd, "platform workspace list")
	var seen string
	leaf.RunE = func(c *cobra.Command, _ []string) error {
		seen, _ = c.Flags().GetString(names.Host)
		return nil
	}
	rootCmd.SetArgs([]string{"platform", cmdWorkspace, cmdList, "--host", "cli-value"})
	require.NoError(t, rootCmd.Execute())
	assert.Equal(t, "cli-value", seen, "CLI flag must override env value")
}

func TestOptInEnvFlags_EnvSatisfiesRequired(t *testing.T) {
	t.Setenv(config.EnvPlatformEnabled, "true")
	t.Setenv(envDevsyHost, "from-env.example.com")
	rootCmd, _ := BuildRoot()
	leaf := resolveCommand(t, rootCmd, "platform workspace list")
	var seen string
	leaf.RunE = func(c *cobra.Command, _ []string) error {
		seen, _ = c.Flags().GetString(names.Host)
		return nil
	}
	rootCmd.SetArgs([]string{"platform", cmdWorkspace, cmdList})
	require.NoError(t, rootCmd.Execute(), "required --host must be satisfied by env")
	assert.Equal(t, "from-env.example.com", seen)
}

// TestOptInEnvFlags_PlatformSubcommandSweep asserts that DEVSY_HOST applies to
// every platform subcommand that exposes --host (and similarly for --project).
// This catches the regression where a future addition to cmd/platform/ forgets
// the inline BindEnv call.
func TestOptInEnvFlags_PlatformSubcommandSweep(t *testing.T) {
	t.Setenv(config.EnvPlatformEnabled, "true")
	for _, env := range []string{envDevsyHost, envDevsyProject} {
		t.Setenv(env, "sweep-"+strings.ToLower(env))
	}
	rootCmd, _ := BuildRoot()
	platformCmd := resolveCommand(t, rootCmd, "platform")

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
	walk(platformCmd)
}

// TestPlatformCommand_HiddenWhenDisabled verifies platform is gated off by default.
func TestPlatformCommand_HiddenWhenDisabled(t *testing.T) {
	t.Setenv(config.EnvPlatformEnabled, "")
	t.Setenv(config.EnvPlatformEnabledLegacy, "")
	rootCmd, _ := BuildRoot()
	for _, c := range rootCmd.Commands() {
		use := c.Use
		if i := strings.IndexByte(use, ' '); i >= 0 {
			use = use[:i]
		}
		require.NotEqual(
			t,
			"platform",
			use,
			"platform command should not be registered when DEVSY_PLATFORM_ENABLED is unset",
		)
	}
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
