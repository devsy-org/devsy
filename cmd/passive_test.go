package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

const (
	passiveRunError          = "run error"
	passiveInvalidDeletion   = "invalid deletion"
	passiveTestCommand       = "passive-fixture"
	passiveTestGroup         = "passive-group"
	passiveTestActive        = "active"
	passiveTestHelpFlag      = "--help"
	passiveTestHomeFlag      = "--home"
	passiveTestUnknownFlag   = "--unknown"
	passiveTestUnknown       = "unknown"
	passiveTestHelp          = "help"
	passiveExternalConfig    = "external config"
	passiveExternalDeletion  = "external pending deletion"
	processConstructionPanic = "construction-panic"
)

func passiveTestRoot(
	run func(*cobra.Command, []string) error,
) (*cobra.Command, *flags.GlobalFlags) {
	root, global := BuildRoot()
	leaf := &cobra.Command{
		Use:     passiveTestCommand,
		Aliases: []string{"pf"},
		Args:    cobra.NoArgs,
		RunE:    run,
	}
	leaf.Flags().String("fixture-value", "", "fixture value")
	flags.MarkPassive(leaf)
	group := &cobra.Command{Use: passiveTestGroup}
	flags.MarkPassive(group)
	group.AddCommand(&cobra.Command{Use: passiveTestActive, RunE: run})
	root.AddCommand(leaf, group)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root, global
}

func TestPassiveDispatch(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{"leaf", []string{passiveTestCommand}, true},
		{"alias", []string{"pf"}, true},
		{"help flag before leaf", []string{passiveTestHelpFlag, passiveTestCommand}, true},
		{"help flag after leaf", []string{passiveTestCommand, passiveTestHelpFlag}, true},
		{"help command", []string{passiveTestHelp, "pf"}, true},
		{"help global value", []string{passiveTestHelp, passiveTestHomeFlag, passiveTestGroup, "pf"}, true},
		{"help malformed topic", []string{passiveTestHelp, passiveTestCommand, passiveTestUnknown}, true},
		{"unknown leaf flag", []string{passiveTestCommand, passiveTestUnknownFlag}, true},
		{"missing leaf flag value", []string{passiveTestCommand, passiveTestHomeFlag}, true},
		{"invalid boolean", []string{passiveTestCommand, "--debug=invalid"}, true},
		{"unexpected argument", []string{passiveTestCommand, passiveTestUnknown}, true},
		{"argument after delimiter", []string{passiveTestCommand, "--", passiveTestGroup}, true},
		{"delimiter before command", []string{"--", passiveTestCommand}, false},
		{"global value is command", []string{passiveTestHomeFlag, passiveTestCommand}, false},
		{"global value before leaf", []string{passiveTestHomeFlag, passiveTestGroup, "pf"}, true},
		{"unmarked child", []string{passiveTestGroup, passiveTestActive}, false},
		{"unmarked child help", []string{passiveTestHelp, passiveTestGroup, passiveTestActive}, false},
		{"marked group", []string{passiveTestGroup}, true},
		{"group unknown child", []string{passiveTestGroup, passiveTestUnknown}, true},
		{"group child consumed as value", []string{passiveTestGroup, passiveTestHomeFlag, passiveTestActive}, true},
		{"completion", []string{"completion", "bash"}, false},
		{"dynamic completion", []string{"__complete", passiveTestCommand, ""}, false},
		{"unknown prefix consumes leaf", []string{passiveTestUnknownFlag, passiveTestCommand}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := passiveTestRoot(nil)
			_, passive := resolveInvocation(root, test.args)
			require.Equal(t, test.want, passive)
		})
	}
}

func passiveFixtureEnvironment(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("HOME", filepath.Join(root, "user"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg-data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg-cache"))
	t.Setenv(config.EnvHome, filepath.Join(root, "ambient"))
	t.Setenv(config.EnvConfig, "")
	return root, filepath.Join(root, "explicit")
}

func preservePassiveFixture(t *testing.T, root string) {
	t.Helper()
	manager := config.DefaultPathManager()
	dependencytest.Preserve(t, root)
	t.Cleanup(func() { require.Same(t, manager, config.DefaultPathManager()) })
}

func TestPassiveRootPreparation(t *testing.T) {
	states := []string{
		"absent root",
		"missing config",
		"saved config",
		passiveExternalConfig,
		passiveRunError,
	}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			rootDir, home := passiveFixtureEnvironment(t)
			configPath := filepath.Join(home, config.ConfigFile)
			if state == passiveExternalConfig {
				configPath = filepath.Join(rootDir, "external", config.ConfigFile)
				t.Setenv(config.EnvConfig, configPath)
			}
			if state != "absent root" {
				require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o700))
			}
			if state == "saved config" || state == passiveExternalConfig {
				writePassiveConfig(t, configPath)
			}
			preservePassiveFixture(t, rootDir)
			called := false
			wantErr := errors.New("passive fixture error")
			root, _ := passiveTestRoot(func(command *cobra.Command, _ []string) error {
				called = true
				return checkPassivePreparedInputs(
					t,
					command,
					passiveExpectedInputs{configPath, home, state},
					wantErr,
				)
			})
			root.SetArgs(
				[]string{
					passiveTestCommand,
					passiveTestHomeFlag,
					home,
					"--context",
					"chosen",
					"--provider",
					"fixture-provider",
				},
			)
			err := root.Execute()
			if state == passiveRunError {
				require.ErrorIs(t, err, wantErr)
			} else {
				require.NoError(t, err)
			}
			require.True(t, called)
		})
	}
}

func mustPassiveDataDir(t *testing.T, paths config.PathManager) string {
	t.Helper()
	data, err := paths.DataDir()
	require.NoError(t, err)
	return data
}

func writePassiveConfig(t *testing.T, path string) {
	t.Helper()
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte("contexts:\n  chosen:\n    options:\n      LOG_LEVEL:\n        value: debug\n"),
			0o600,
		),
	)
}

func TestPassiveRootPreparationErrors(t *testing.T) {
	states := []string{
		"malformed config",
		"pending deletion",
		passiveInvalidDeletion,
		passiveExternalDeletion,
	}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			rootDir, home := passiveFixtureEnvironment(t)
			require.NoError(t, os.MkdirAll(home, 0o700))
			path := filepath.Join(home, config.ConfigFile)
			if state == passiveExternalDeletion {
				path = filepath.Join(rootDir, "external.yaml")
				t.Setenv(config.EnvConfig, path)
			}
			require.NoError(t, os.WriteFile(path, []byte("contexts: ["), 0o600))
			if state != "malformed config" {
				writePassiveIntent(t, home, path, state == passiveInvalidDeletion)
			}
			preservePassiveFixture(t, rootDir)
			root, _ := passiveTestRoot(func(*cobra.Command, []string) error {
				t.Fatal("RunE reached after preparation failure")
				return nil
			})
			root.SetArgs([]string{passiveTestCommand, passiveTestHomeFlag, home})
			err := root.Execute()
			require.Error(t, err)
			switch state {
			case "pending deletion", passiveExternalDeletion:
				require.ErrorIs(t, err, config.ErrContextDeletionPending)
			case passiveInvalidDeletion:
				require.ErrorIs(t, err, config.ErrContextDeletionIntentInvalid)
			default:
				require.Contains(t, err.Error(), "yaml")
			}
		})
	}
}

func writePassiveIntent(t *testing.T, home, path string, invalid bool) {
	t.Helper()
	intent := config.ContextDeletionIntent{
		SchemaVersion: 1, ConfigFile: path, StoreDirectory: filepath.Dir(path),
		DataDirectory: home, Context: "chosen", Fingerprint: strings.Repeat("a", 64),
	}
	raw, err := json.Marshal(intent)
	require.NoError(t, err)
	if invalid {
		raw = []byte(`{"private":"invalid-intent-fixture"`)
	}
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(filepath.Dir(path), config.ContextDeletionIntentFile),
			raw,
			0o600,
		),
	)
}

func TestPassiveUnmarkedChildUsesOrdinaryHooks(t *testing.T) {
	_, home := passiveFixtureEnvironment(t)
	root, _ := passiveTestRoot(func(command *cobra.Command, _ []string) error {
		require.Equal(t, home, os.Getenv(config.EnvHome))
		_, ok := flags.PassiveInputsFromContext(command.Context())
		require.False(t, ok)
		return nil
	})
	root.SetArgs([]string{passiveTestGroup, passiveTestActive, passiveTestHomeFlag, home})
	require.NoError(t, root.Execute())
	_, exists := os.LookupEnv(config.EnvHome)
	require.False(t, exists, "ordinary post-run retains its existing Unsetenv behavior")
	_, err := os.Stat(home)
	require.NoError(t, err)
}

func TestPassiveSandboxPolicy(t *testing.T) {
	require.NoError(t, passiveHostInspection(false))
	require.ErrorContains(t, passiveHostInspection(true), "unsupported inside Flatpak")
}

func TestPassivePreparseOutput(t *testing.T) {
	rootDir, _ := passiveFixtureEnvironment(t)
	preservePassiveFixture(t, rootDir)
	root, global := passiveTestRoot(nil)
	root.SetContext(withInvocationPolicy(context.Background(), true))
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{config.BinaryName, passiveTestCommand}
	configureOutput(root, global, false)
	require.Equal(t, log.DefaultLevel, defaultPassiveLevel(t, root, global))
	require.Contains(t, telemetryCollectorType(root), "noop")
}

func defaultPassiveLevel(t *testing.T, root *cobra.Command, global *flags.GlobalFlags) string {
	t.Helper()
	returnLevel, err := preparePassive(root, global)
	require.NoError(t, err)
	return returnLevel
}

func telemetryCollectorType(command *cobra.Command) string {
	return fmt.Sprintf("%T", telemetry.FromContext(command.Context()))
}

func checkPassivePreparedInputs(
	t *testing.T,
	command *cobra.Command,
	expected passiveExpectedInputs,
	wantErr error,
) error {
	t.Helper()
	inputs, ok := flags.PassiveInputsFromContext(command.Context())
	require.True(t, ok)
	path, err := inputs.Paths.ConfigFilePath()
	require.NoError(t, err)
	require.Equal(t, expected.configPath, path)
	require.Equal(t, expected.configPath, inputs.Config.Origin)
	require.Equal(t, "chosen", inputs.Config.DefaultContext)
	require.Equal(
		t,
		"fixture-provider",
		inputs.Config.Contexts["chosen"].DefaultProvider,
	)
	require.Equal(t, expected.home, mustPassiveDataDir(t, inputs.Paths))
	if strings.Contains(expected.state, "config") && expected.state != "missing config" {
		require.Equal(
			t,
			"debug",
			inputs.Config.ContextOption(config.ContextOptionLogLevel),
		)
		require.True(t, log.DebugEnabled())
	}
	if expected.state == passiveRunError {
		return wantErr
	}
	return nil
}

type passiveExpectedInputs struct {
	configPath string
	home       string
	state      string
}
