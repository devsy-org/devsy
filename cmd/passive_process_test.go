package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/telemetry/analytics"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

const (
	processFixtureEnv     = "DEVSY_T10_PROCESS_FIXTURE"
	processFixtureWindows = "windows"
)

// This harness runs the outer production lifecycle with test-only commands.
// It cannot replace a shipped main invocation once real passive commands land.
func TestPassiveProcessLifecycle(t *testing.T) {
	binary := buildPassiveProcessHelper(t)
	var requests atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "us.i.posthog.com:443" {
			t.Errorf("unexpected proxy target: %s %s", r.Method, r.Host)
		}
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()

	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{"success", []string{passiveTestCommand}, 0},
		{"run-error", []string{passiveTestCommand}, 1},
		{"panic", []string{passiveTestCommand}, 1},
		{processConstructionPanic, []string{passiveTestCommand}, 1},
		{"alias", []string{"pf"}, 0},
		{passiveTestHelp, []string{passiveTestCommand, passiveTestHelpFlag}, 0},
		{"help-before", []string{passiveTestHelpFlag, passiveTestCommand}, 0},
		{"help-command", []string{passiveTestHelp, "pf"}, 0},
		{"unknown-flag", []string{passiveTestCommand, passiveTestUnknownFlag}, 1},
		{"missing-value", []string{passiveTestCommand, passiveTestHomeFlag}, 1},
		{"bad-value", []string{passiveTestCommand, "--debug=invalid"}, 1},
		{"after-delimiter", []string{passiveTestCommand, "--", passiveTestActive}, 1},
		{"malformed-config", []string{passiveTestCommand}, 1},
		{"pending-deletion", []string{passiveTestCommand}, 1},
		{"invalid-deletion", []string{passiveTestCommand}, 1},
		{"external-config", []string{passiveTestCommand}, 0},
		{"marked-group-help", []string{passiveTestHelp, passiveTestGroup}, 0},
		{"unknown-group-child-help", []string{passiveTestGroup, passiveTestUnknown}, 0},
		{"flag-value-before-leaf", []string{passiveTestHomeFlag, passiveTestGroup, "pf"}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPassiveProcessFixture(t, test.name, proxy.URL)
			before := requests.Load()
			output, code := fixture.run(t, binary, test.args)
			require.Equal(t, test.code, code, "%s", output)
			require.Equal(t, before, requests.Load(), "unexpected proxy request")
			require.NoFileExists(t, fixture.sentinel)
			require.Contains(t, output, "T10_HARNESS_PRESERVED")
		})
	}

	testPassiveProcessControls(t, binary, proxy.URL, &requests)
}

func testPassiveProcessControls(t *testing.T, binary, proxyURL string, requests *atomic.Int64) {
	t.Helper()

	t.Run("flatpak-sentinel-positive-control", func(t *testing.T) {
		if runtime.GOOS == processFixtureWindows {
			t.Skip("shell sentinel is Unix-only")
		}
		fixture := newPassiveProcessFixture(t, "sentinel-control", proxyURL)
		output, code := fixture.run(t, binary, []string{passiveTestCommand})
		require.Zero(t, code, "%s", output)
		raw, err := os.ReadFile(filepath.Clean(fixture.sentinel))
		require.NoError(t, err)
		require.Contains(t, string(raw), "flatpak-spawn")
	})

	t.Run("ordinary-positive-control", func(t *testing.T) {
		fixture := newPassiveProcessFixture(t, "ordinary", proxyURL)
		before := requests.Load()
		output, code := fixture.run(t, binary, []string{passiveTestGroup, passiveTestActive})
		require.Zero(t, code, "%s", output)
		require.Greater(
			t,
			requests.Load(),
			before,
			"telemetry proxy trap must detect ordinary recording",
		)
		if runtime.GOOS == "darwin" {
			raw, err := os.ReadFile(filepath.Clean(fixture.sentinel))
			require.NoError(t, err)
			require.Contains(t, string(raw), "ioreg")
		}
	})
}

func buildPassiveProcessHelper(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "t10-helper")
	if runtime.GOOS == processFixtureWindows {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	// #nosec G204 -- only the test-owned output path varies in this fixed build command.
	cmd := exec.CommandContext(
		ctx,
		"go",
		"test",
		"-c",
		"-race",
		"-o",
		binary,
		"-ldflags=-X github.com/devsy-org/devsy/pkg/version.version=v0.99.0-t10-fixture "+
			"-X github.com/devsy-org/devsy/pkg/telemetry/analytics.posthogAPIKey=test",
		".",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return binary
}

type passiveProcessFixture struct {
	environment []string
	sentinel    string
}

func newPassiveProcessFixture(t *testing.T, mode, proxy string) passiveProcessFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	homes := filepath.Join(root, "homes")
	require.NoError(t, os.Mkdir(homes, 0o700))
	home := filepath.Join(homes, "explicit")
	sentinel := filepath.Join(root, "process-attempts")
	environment := passiveProcessEnvironment(homes)
	environment = append(environment,
		processFixtureEnv+"="+mode, "DEVSY_T10_TREE="+homes, "DEVSY_T10_HOME="+home,
		"HTTPS_PROXY="+proxy, "HTTP_PROXY="+proxy, "NO_PROXY=", "ALL_PROXY="+proxy,
		"https_proxy="+proxy, "http_proxy="+proxy, "no_proxy=", "all_proxy="+proxy,
	)
	if runtime.GOOS != processFixtureWindows {
		bin := filepath.Join(root, "bin")
		require.NoError(t, os.Mkdir(bin, 0o700))
		for _, name := range []string{"ioreg", "flatpak-spawn"} {
			script := fmt.Sprintf(
				"#!/bin/sh\nprintf '%%s\\n' '%s' >> '%s'\nprintf '\"IOPlatformUUID\" = \"T10-FIXTURE-ID\"\\n'\n",
				name,
				sentinel,
			)
			// #nosec G306 -- these test-owned process sentinels must be executable.
			require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700))
		}
		environment = append(environment, "PATH="+bin)
	}
	if strings.Contains(mode, "config") || strings.Contains(mode, "deletion") {
		require.NoError(t, os.Mkdir(home, 0o700))
		path := filepath.Join(home, config.ConfigFile)
		if mode == "external-config" {
			path = filepath.Join(homes, "external-config.yaml")
			environment = append(environment, config.EnvConfig+"="+path)
		}
		require.NoError(t, os.WriteFile(path, []byte("contexts: {}"), 0o600))
		if mode == "malformed-config" {
			require.NoError(t, os.WriteFile(path, []byte("contexts: ["), 0o600))
		}
		if strings.Contains(mode, "deletion") {
			writePassiveIntent(t, home, path, mode == "invalid-deletion")
		}
	}
	return passiveProcessFixture{environment: environment, sentinel: sentinel}
}

func passiveProcessEnvironment(homes string) []string {
	removed := []string{
		"CI",
		"TRAVIS",
		"GITHUB_ACTIONS",
		"GITLAB_CI",
		"CIRCLECI",
		"TEAMCITY_VERSION",
		"BITBUCKET_BUILD_NUMBER",
		config.EnvHome,
		config.EnvConfig,
		config.EnvDisableTelemetry,
		config.EnvUI,
		config.EnvPlatformRunner,
		"HOME",
		"USERPROFILE",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_CACHE_HOME",
		"PATH",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"ALL_PROXY",
		"http_proxy",
		"https_proxy",
		"no_proxy",
		"all_proxy",
	}
	environment := slices.DeleteFunc(os.Environ(), func(value string) bool {
		key, _, _ := strings.Cut(value, "=")
		return slices.Contains(removed, key) || strings.HasPrefix(key, "DEVSY_")
	})
	for key, subdir := range map[string]string{
		"HOME": "user", "USERPROFILE": "user", config.EnvHome: "ambient",
		"XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache",
	} {
		environment = append(environment, key+"="+filepath.Join(homes, subdir))
	}
	return append(environment, config.EnvDisableTelemetry+"=false", config.EnvConfig+"=")
}

func (fixture passiveProcessFixture) run(t *testing.T, binary string, args []string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// #nosec G204 -- binary and argv belong to this isolated test fixture.
	cmd := exec.CommandContext(
		ctx,
		binary,
		append([]string{"-test.run=^TestPassiveProcessHelper$", "--"}, args...)...)
	cmd.Env = fixture.environment
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), "helper timeout: %.4096s", output)
	if err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		return string(output), exit.ExitCode()
	}
	return string(output), 0
}

func TestPassiveProcessHelper(t *testing.T) {
	mode := os.Getenv(processFixtureEnv)
	if mode == "" {
		return
	}
	require.NotEqual(t, version.DevVersion, version.GetVersion())
	require.NotEqual(t, config.BoolTrue, os.Getenv(config.EnvDisableTelemetry))
	require.False(t, analytics.Dry)
	if mode == "sentinel-control" {
		cmd := exec.CommandContext(t.Context(), "flatpak-spawn", "--fixture")
		require.NoError(t, cmd.Run())
		return
	}
	index := slices.Index(os.Args, "--")
	require.Positive(t, index)
	os.Args = append([]string{config.BinaryName}, os.Args[index+1:]...)
	if mode != "missing-value" && mode != processConstructionPanic {
		os.Args = append(os.Args, passiveTestHomeFlag, os.Getenv("DEVSY_T10_HOME"))
	}
	environment := dependencytest.Environment()
	tree, err := dependencytest.Tree(os.Getenv("DEVSY_T10_TREE"))
	require.NoError(t, err)
	manager := config.DefaultPathManager()
	code := runWithRootFactory(func() (*cobra.Command, *flags.GlobalFlags) {
		return processFixtureRoot(t, mode)
	})
	if mode != "ordinary" {
		require.Equal(t, environment, dependencytest.Environment())
		after, err := dependencytest.Tree(os.Getenv("DEVSY_T10_TREE"))
		require.NoError(t, err)
		require.Equal(t, tree, after)
		require.Same(t, manager, config.DefaultPathManager())
		_, err = fmt.Fprintln(os.Stdout, "T10_HARNESS_PRESERVED")
		require.NoError(t, err)
	}
	os.Exit(code)
}

func processFixtureRoot(t *testing.T, mode string) (*cobra.Command, *flags.GlobalFlags) {
	t.Helper()
	if mode == processConstructionPanic {
		panic("construction fixture")
	}
	return passiveTestRoot(func(command *cobra.Command, _ []string) error {
		if mode != "ordinary" {
			require.Contains(
				t,
				fmt.Sprintf("%T", telemetry.FromContext(command.Context())),
				"noop",
			)
		}
		switch mode {
		case "run-error":
			return errors.New("RunE fixture")
		case "panic":
			panic("RunE fixture")
		default:
			return nil
		}
	})
}
