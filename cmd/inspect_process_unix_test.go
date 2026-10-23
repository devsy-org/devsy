//go:build darwin || linux

package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/providers"
	"github.com/stretchr/testify/require"
)

type inspectProcessFixture struct {
	root, home, marker string
	environment        []string
	paths              config.PathManager
	snapshot           *config.Config
}

func newInspectProcessFixture(t *testing.T, proxy string) *inspectProcessFixture {
	t.Helper()
	t.Setenv(config.EnvConfig, "")
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home := filepath.Join(root, "explicit")
	fixture := &inspectProcessFixture{
		root:   root,
		home:   home,
		marker: filepath.Join(root, "EXECUTED"),
	}
	fixture.paths = config.NewReadOnlyPathManagerForHome(home)
	fixture.snapshot = &config.Config{
		SchemaVersion:  1,
		DefaultContext: inspectTestContext,
		Contexts: map[string]*config.ContextConfig{
			inspectTestContext: {
				DefaultProvider: "docker-installed",
				Providers:       map[string]*config.ProviderConfig{},
			},
		},
	}
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o700))
	for _, name := range []string{inspectTestDocker, inspectTestPodman, "container", "ioreg", "flatpak-spawn"} {
		script := fmt.Sprintf(
			"#!/bin/sh\nprintf '%%s\\n' '%s' >> '%s'\nprintf '\"IOPlatformUUID\" = \"T10-FIXTURE-ID\"\\n'\n",
			name,
			fixture.marker,
		)
		// #nosec G306 -- test-owned executable sentinels.
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700))
	}
	fixture.installProviders(t, bin)
	fixture.environment = append(
		passiveProcessEnvironment(root),
		"PATH="+bin,
		"HTTP_PROXY="+proxy,
		"HTTPS_PROXY="+proxy,
		"NO_PROXY=",
		"ALL_PROXY="+proxy,
		"http_proxy="+proxy,
		"https_proxy="+proxy,
		"no_proxy=",
		"all_proxy="+proxy,
		"DOCKER_PATH=/environment/decoy",
		"DEVSY_PROVIDER_DOCKER-INSTALLED_DOCKER_PATH=/provider/decoy",
	)
	// Most fixtures retain a preexisting ambient cache to isolate command behavior.
	// The separate fresh-root case verifies startup does not create that cache.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ambient", "cache"), 0o700))
	return fixture
}

func (fixture *inspectProcessFixture) installProviders(t *testing.T, bin string) {
	t.Helper()
	bindings := map[string]string{
		inspectTestDocker: "DOCKER_PATH", inspectTestPodman: "PODMAN_PATH",
		inspectTestApple: "CONTAINER_PATH", inspectTestKubernetes: "KUBERNETES_CONFIG",
	}
	for builtin, option := range bindings {
		name := builtin + "-installed"
		manifest, err := provider.ParseProvider(
			strings.NewReader(providers.GetBuiltInProviders()[builtin]),
		)
		require.NoError(t, err)
		manifest.Name, manifest.Source = name, provider.ProviderSource{Internal: true, Raw: builtin}
		fixture.writeManifest(t, name, manifest)
		path := filepath.Join(bin, builtin)
		if builtin == inspectTestApple {
			path = filepath.Join(bin, "container")
		}
		if builtin == inspectTestKubernetes {
			path = filepath.Join(fixture.root, "unparsed-kubeconfig")
			require.NoError(
				t,
				os.WriteFile(
					path,
					[]byte("malformed: [\ncredential-plugin: NEVER_EXECUTE\n"),
					0o600,
				),
			)
		}
		fixture.snapshot.Current().Providers[name] = &config.ProviderConfig{
			Options: map[string]config.OptionValue{
				option: {
					Value:        path,
					UserProvided: true,
				},
				"DOCKER_HOST": {Value: "https://credential@127.0.0.1:1"},
			},
		}
	}
	fixture.save(t)
}

func (fixture *inspectProcessFixture) writeManifest(
	t *testing.T,
	name string,
	manifest *provider.ProviderConfig,
) {
	t.Helper()
	dir, err := fixture.paths.ProviderDir(inspectTestContext, name)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, provider.ProviderConfigFile), raw, 0o600))
}

func (fixture *inspectProcessFixture) save(t *testing.T) {
	t.Helper()
	path, err := fixture.paths.ConfigFilePath()
	require.NoError(t, err)
	raw, err := json.Marshal(fixture.snapshot)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}

func buildInspectMain(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("DEVSY_T10_EVIDENCE_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	// #nosec G703 -- opt-in evidence directory belongs to the test runner.
	require.NoError(t, os.MkdirAll(dir, 0o700))
	binary := filepath.Join(dir, "devsy-inspect-main")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	// #nosec G204 G702 -- fixed repository-main build; runner controls only the output path.
	command := exec.CommandContext(ctx, "go", "build", "-o", binary,
		"-ldflags=-X github.com/devsy-org/devsy/pkg/version.version=v0.99.0-t10-inspect "+
			"-X github.com/devsy-org/devsy/pkg/telemetry/analytics.posthogAPIKey=test", ".")
	command.Dir = ".."
	buildOutput, err := command.CombinedOutput()
	require.NoError(t, err, "%s", buildOutput)
	// #nosec G304 G703 -- read back the actual binary built in the test-owned evidence directory.
	raw, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	t.Logf("actual main binary: %s sha256=%s", binary, hex.EncodeToString(digest[:]))
	return binary
}

func (fixture *inspectProcessFixture) run(
	t *testing.T,
	binary string,
	args []string,
) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	master, tty, err := pty.Open()
	require.NoError(t, err)
	defer func() { require.NoError(t, master.Close()) }()
	defer func() { _ = tty.Close() }()
	// #nosec G204 -- only isolated fixture argv and the actual test-built main binary.
	command := exec.CommandContext(
		ctx,
		binary,
		append([]string{passiveTestHomeFlag, fixture.home}, args...)...)
	command.Env, command.Dir = fixture.environment, fixture.root
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, tty
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(&stderr, master); close(copied) }()
	err = command.Run()
	require.NoError(t, tty.Close())
	<-copied
	require.NoError(t, ctx.Err(), "shipped-main timeout")
	code := 0
	if err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		code = exit.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func saveInspectEvidence(t *testing.T, name, stdout, stderr string) {
	t.Helper()
	dir := os.Getenv("DEVSY_T10_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	// #nosec G703 -- runner-owned evidence directory and literal case names.
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".stdout"), []byte(stdout), 0o600))
	// #nosec G703 -- runner-owned evidence directory and literal case names.
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".stderr"), []byte(stderr), 0o600))
}

func decodeInspectReport(t *testing.T, stdout string) dependencies.Report {
	t.Helper()
	var report dependencies.Report
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&report))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
	require.True(t, strings.HasSuffix(stdout, "\n"))
	require.Equal(t, "devsy.dependencies.report.v1", report.Schema)
	require.Empty(t, report.Actions)
	require.False(t, report.Executed)
	require.Equal(t, "unknown", report.Compatibility)
	require.Equal(t, "not_checked", report.Reachability)
	require.Equal(t, "not_required", report.Host.VirtualizationCapability)
	return report
}

func TestInspectShippedMain(t *testing.T) {
	binary := buildInspectMain(t)
	var requests atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	for _, test := range []struct{ builtin, tool string }{
		{inspectTestDocker, inspectTestDocker},
		{inspectTestPodman, inspectTestPodman},
		{inspectTestApple, "apple-container"},
		{inspectTestKubernetes, "kubernetes-config"},
	} {
		t.Run(test.builtin, func(t *testing.T) {
			fixture := newInspectProcessFixture(t, proxy.URL)
			dependencytest.Preserve(t, fixture.root)
			before := requests.Load()
			stdout, stderr, code := fixture.run(
				t,
				binary,
				[]string{
					inspectTestGroup,
					inspectTestLeaf,
					test.tool,
					inspectTestForProvider,
					test.builtin + "-installed",
					inspectTestJSON,
				},
			)
			require.Zero(t, code, "%s", stderr)
			require.Empty(t, stderr)
			report := decodeInspectReport(t, stdout)
			require.Equal(t, test.builtin, report.Target.ProviderType)
			require.Equal(t, test.builtin+"-installed", report.Target.ProviderName)
			require.Equal(t, "unknown", report.Availability)
			require.Equal(t, before, requests.Load())
			require.NoFileExists(t, fixture.marker)
			saveInspectEvidence(t, "report-"+test.builtin, stdout, stderr)
		})
	}
	testInspectMainCases(t, binary, proxy.URL, &requests)
	testInspectMainControls(t, binary, proxy.URL, &requests)
	testInspectFreshRoots(t, binary, proxy.URL)
}

func testInspectFreshRoots(t *testing.T, binary, proxy string) {
	t.Helper()
	t.Run("fresh-roots-preservation", func(t *testing.T) {
		fixture := newInspectProcessFixture(t, proxy)
		require.NoError(t, os.Remove(filepath.Join(fixture.root, "ambient", "cache")))
		require.NoError(t, os.Remove(filepath.Join(fixture.root, "ambient")))
		before, err := dependencytest.Tree(fixture.root)
		require.NoError(t, err)
		stdout, stderr, code := fixture.run(
			t,
			binary,
			[]string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
		)
		require.Zero(t, code, "%s", stderr)
		decodeInspectReport(t, stdout)
		after, err := dependencytest.Tree(fixture.root)
		require.NoError(t, err)
		beforeJSON, err := json.Marshal(before)
		require.NoError(t, err)
		afterJSON, err := json.Marshal(after)
		require.NoError(t, err)
		saveInspectEvidence(t, "fresh-roots", stdout, stderr)
		saveInspectEvidence(t, "fresh-roots-trees", string(beforeJSON), string(afterJSON))
		if !bytes.Equal(beforeJSON, afterJSON) {
			t.Error(
				"actual main changed fresh ambient roots before Execute; " +
					"pkg/platform/client.init calls PlatformCacheDir (retained before/after tree evidence)",
			)
		}
	})
}

//nolint:funlen // The literal argv and fixture matrix keeps framing cases reviewable together.
func testInspectMainCases(t *testing.T, binary, proxy string, requests *atomic.Int64) {
	t.Helper()
	for _, test := range []struct {
		name                      string
		args                      []string
		change                    func(*testing.T, *inspectProcessFixture)
		help, failure, humanError bool
	}{
		{name: "default", args: []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON}},
		{
			name: "provider-env-default",
			args: []string{inspectTestGroup, inspectTestLeaf, inspectTestPodman, inspectTestJSON},
			change: func(_ *testing.T, f *inspectProcessFixture) {
				f.environment = append(f.environment, "DEVSY_PROVIDER=podman-installed")
			},
		},
		{
			name: "env-decoy-override",
			args: []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestDockerTarget, inspectTestJSON},
			change: func(_ *testing.T, f *inspectProcessFixture) {
				f.environment = append(f.environment, "DEVSY_PROVIDER=podman-installed")
			},
		},
		{
			name: "cli-conflict",
			args: []string{
				inspectTestGroup,
				inspectTestLeaf,
				inspectTestDocker,
				"--provider=podman-installed",
				inspectTestDockerTarget,
				inspectTestJSON,
			},
			failure: true,
		},
		{
			name: "cli-empty-conflict",
			args: []string{
				inspectTestGroup,
				inspectTestLeaf,
				inspectTestDocker,
				"--provider=",
				inspectTestDockerTarget,
				inspectTestJSON,
			},
			failure: true,
		},
		{
			name:    "empty-target",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, "--for-provider=", inspectTestJSON},
			failure: true,
		},
		{
			name: "mismatch",
			args: []string{inspectTestGroup, inspectTestLeaf, inspectTestPodman, inspectTestJSON}, failure: true,
		},
		{
			name:    "unknown-before-json",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, "--bad", inspectTestJSON},
			failure: true,
		},
		{
			name:    "bad-bool-before-json",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, "--debug=bad", inspectTestJSON},
			failure: true,
		},
		{
			name:    "invalid-json",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestBadJSON},
			failure: true,
		},
		{
			name:    "invalid-json-then-false",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestBadJSON, inspectTestJSONFalse},
			failure: true,
		},
		{
			name: "json-false",
			args: []string{
				inspectTestGroup, inspectTestLeaf, inspectTestDocker,
				inspectTestJSON, inspectTestJSONFalse, "--bad",
			},
			failure:    true,
			humanError: true,
		},
		{
			name:       "json-value",
			args:       []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestForProvider, inspectTestJSON},
			failure:    true,
			humanError: true,
		},
		{
			name:       "json-equals-value",
			args:       []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, "--for-provider=--json"},
			failure:    true,
			humanError: true,
		},
		{
			name:       "json-operand",
			args:       []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, "--", inspectTestJSON},
			failure:    true,
			humanError: true,
		},
		{
			name: "json-false-result-mode",
			args: []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSONFalse, "--result-format=json"},
		},
		{name: "missing-tool", args: []string{inspectTestGroup, inspectTestLeaf, inspectTestJSON}, failure: true},
		{
			name:    "bad-log-level",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, "--log-level=credential", inspectTestJSON},
			failure: true,
		},
		{
			name: passiveTestHelp,
			args: []string{inspectTestGroup, inspectTestLeaf, passiveTestHelpFlag, inspectTestJSON}, help: true,
		},
		{name: "help-target", args: []string{passiveTestHelp, inspectTestGroup, inspectTestLeaf}, help: true},
		{name: "group-help", args: []string{inspectTestGroup, passiveTestHelpFlag}, help: true},
		{
			name: "missing",
			args: []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			change: func(t *testing.T, f *inspectProcessFixture) {
				f.snapshot.Current().Providers["docker-installed"].Options["DOCKER_PATH"] = config.OptionValue{
					Value: filepath.Join(f.root, "absent"), UserProvided: true,
				}
				f.save(t)
			},
		},
		{
			name: "empty-path",
			args: []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			change: func(t *testing.T, f *inspectProcessFixture) {
				f.snapshot.Current().Providers["docker-installed"].Options["DOCKER_PATH"] = config.OptionValue{UserProvided: true}
				f.save(t)
			},
		},
		{
			name:    "unresolved",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			failure: true,
			change: func(t *testing.T, f *inspectProcessFixture) {
				value := f.snapshot.Current().Providers["docker-installed"].Options["DOCKER_PATH"]
				value.Children = []string{"DOCKER_PATH"}
				f.snapshot.Current().Providers["docker-installed"].Options["DOCKER_PATH"] = value
				f.save(t)
			},
		},
		{
			name:    "missing-record",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			failure: true,
			change: func(t *testing.T, f *inspectProcessFixture) {
				delete(f.snapshot.Current().Providers, "docker-installed")
				f.save(t)
			},
		},
		{
			name:    "missing-manifest",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			failure: true,
			change: func(t *testing.T, f *inspectProcessFixture) {
				dir, err := f.paths.ProviderDir(inspectTestContext, "docker-installed")
				require.NoError(t, err)
				require.NoError(t, os.Remove(filepath.Join(dir, provider.ProviderConfigFile)))
			},
		},
		{
			name:    "untrusted",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			failure: true,
			change: func(t *testing.T, f *inspectProcessFixture) {
				// #nosec G101 -- synthetic credential proves source errors are bounded.
				f.writeManifest(t, "docker-installed", &provider.ProviderConfig{
					Name:   "docker-installed",
					Source: provider.ProviderSource{URL: "https://user:credential@example.test/provider"},
				})
			},
		},
		{
			name:    "inspect-malformed-config",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			failure: true,
			change: func(t *testing.T, f *inspectProcessFixture) {
				path, err := f.paths.ConfigFilePath()
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte("contexts: [credential"), 0o600))
			},
		},
		{
			name:    "pending-deletion",
			args:    []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			failure: true,
			change: func(t *testing.T, f *inspectProcessFixture) {
				path, err := f.paths.ConfigFilePath()
				require.NoError(t, err)
				writePassiveIntent(t, f.home, path, false)
			},
		},
		{
			name: "context-override",
			args: []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, "--context=chosen", inspectTestJSON},
		},
		{
			name: "inspect-external-config",
			args: []string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
			change: func(t *testing.T, f *inspectProcessFixture) {
				path := filepath.Join(f.root, "external-config.json")
				raw, err := json.Marshal(f.snapshot)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, raw, 0o600))
				f.environment = append(f.environment, config.EnvConfig+"="+path)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newInspectProcessFixture(t, proxy)
			if test.change != nil {
				test.change(t, fixture)
			}
			dependencytest.Preserve(t, fixture.root)
			before := requests.Load()
			stdout, stderr, code := fixture.run(t, binary, test.args)
			switch {
			case test.failure:
				require.Equal(t, 1, code, "%s %s", stdout, stderr)
				require.Empty(t, stdout)
				if test.humanError {
					require.False(t, json.Valid([]byte(stderr)), "PTY stderr: %s", stderr)
					require.Contains(t, stderr, "Error:")
				} else {
					var envelope map[string]any
					require.NoError(
						t,
						json.Unmarshal([]byte(stderr), &envelope),
						"PTY stderr: %s",
						stderr,
					)
				}
				require.NotContains(t, stderr, "credential")
				require.NotContains(t, stderr, fixture.root)
			case test.help:
				require.Zero(t, code, "%s", stderr)
				require.Contains(t, stdout, "USAGE:")
				require.NotContains(t, stdout, "devsy.dependencies.report.v1")
			default:
				require.Zero(t, code, "%s", stderr)
				report := decodeInspectReport(t, stdout)
				if test.name == "missing" {
					require.Equal(t, "missing", report.Availability)
				}
			}
			require.Equal(t, before, requests.Load())
			require.NoFileExists(t, fixture.marker)
			saveInspectEvidence(t, test.name, stdout, stderr)
		})
	}
}

func testInspectMainControls(t *testing.T, binary, proxy string, requests *atomic.Int64) {
	t.Helper()
	fixture := newInspectProcessFixture(t, proxy)
	stdout, stderr, code := fixture.run(t, binary, []string{"--version"})
	require.Zero(t, code, "%s", stderr)
	require.Contains(t, stdout, "v0.99.0-t10-inspect")
	saveInspectEvidence(t, "version-control", stdout, stderr)
	before := requests.Load()
	stdout, stderr, code = fixture.run(
		t,
		binary,
		[]string{"provider", cmdList, "--result-format=json"},
	)
	require.Zero(t, code, "%s", stderr)
	require.Greater(
		t,
		requests.Load(),
		before,
		"ordinary production command must reach rejecting telemetry proxy",
	)
	if runtime.GOOS == "darwin" {
		raw, err := os.ReadFile(fixture.marker)
		require.NoError(t, err)
		require.Contains(t, string(raw), "ioreg")
	}
	saveInspectEvidence(t, "ordinary-control", stdout, stderr)
	// Exercise the flatpak-spawn trap directly; no native Flatpak claim follows.
	// #nosec G204 -- execute only this fixture-owned positive-control sentinel.
	control := exec.CommandContext(t.Context(), filepath.Join(fixture.root, "bin", "flatpak-spawn"))
	require.NoError(t, control.Run())
	raw, err := os.ReadFile(fixture.marker)
	require.NoError(t, err)
	require.Contains(t, string(raw), "flatpak-spawn")
	fixture = newInspectProcessFixture(t, proxy)
	fixture.environment = append(fixture.environment, "DEVSY_DEBUG=garbage")
	stdout, stderr, code = fixture.run(
		t,
		binary,
		[]string{inspectTestGroup, inspectTestLeaf, inspectTestDocker, inspectTestJSON},
	)
	require.NotZero(t, code)
	require.Empty(t, stdout)
	require.False(
		t,
		json.Valid([]byte(stderr)),
		"constructor failures remain outside inspection framing",
	)
	saveInspectEvidence(t, "constructor-exclusion", stdout, stderr)
}
