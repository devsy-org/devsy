//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type recordedCommand struct {
	path    string
	args    []string
	timeout time.Duration
}
type fakeRunner struct {
	calls []recordedCommand
	fn    func(recordedCommand) CommandResult
}

func (r *fakeRunner) Run(_ context.Context, spec CommandSpec) CommandResult {
	c := recordedCommand{
		path:    spec.Path,
		args:    append([]string(nil), spec.Args...),
		timeout: spec.Timeout,
	}
	r.calls = append(r.calls, c)
	if r.fn != nil {
		return r.fn(c)
	}
	return successResult("")
}

func successResult(out string) CommandResult {
	code := 0
	return CommandResult{ExitCode: &code, Stdout: out}
}

func failureResult(out string) CommandResult {
	code := 1
	return CommandResult{ExitCode: &code, Stderr: out}
}

func commandHas(r *fakeRunner, match func(recordedCommand) bool) bool {
	return slices.ContainsFunc(r.calls, match)
}

type testFS struct {
	reads   map[string][]byte
	appends map[string]*testWriter
}

func (f *testFS) ReadFile(path string) ([]byte, error) {
	b, ok := f.reads[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return b, nil
}
func (f *testFS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }
func (f *testFS) OpenAppend(path string) (io.WriteCloser, error) {
	w := &testWriter{}
	f.appends[path] = w
	return w, nil
}

type testWriter struct{ strings.Builder }

func (w *testWriter) Close() error { return nil }

type linuxFixture struct {
	cfg    SetupConfig
	runner *fakeRunner
	files  *testFS
	clock  *fakeClock
}

func testSetup(t *testing.T, mode Mode) linuxFixture {
	t.Helper()
	dir := t.TempDir()
	podman := filepath.Join(dir, "podman")
	crun := filepath.Join(dir, "crun")
	if err := os.WriteFile(podman, []byte(""), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(crun, []byte(""), 0o700); err != nil {
		t.Fatal(err)
	}
	return linuxFixture{cfg: SetupConfig{
		Mode:             mode,
		PodmanPath:       podman,
		CrunPath:         crun,
		BootstrapTimeout: time.Minute,
		GitHubEnvPath:    filepath.Join(dir, "github-env"),
	}, runner: &fakeRunner{}, files: &testFS{
		reads:   map[string][]byte{},
		appends: map[string]*testWriter{},
	}, clock: &fakeClock{
		now: time.Unix(100, 0),
	}}
}

func testDeps(r *fakeRunner, files *testFS, clock *fakeClock) Dependencies {
	return Dependencies{
		Runner: r,
		Files:  files,
		Clock:  clock,
		Log:    &Logger{Out: io.Discard, Err: io.Discard},
	}
}

func TestValidateConfig(t *testing.T) {
	cfg := testSetup(t, ModeRootless).cfg
	if err := validateConfig(cfg, "linux"); err != nil {
		t.Fatal(err)
	}
	cfg.Mode = "invalid"
	usage, ok := errors.AsType[*ExitError](validateConfig(cfg, goosLinux))
	if !ok || usage.Code != 2 {
		t.Fatalf("invalid mode error = %v", usage)
	}
	cfg.Mode = ModeRootless
	cfg.CrunPath = ""
	if err := validateConfig(cfg, "linux"); err == nil {
		t.Fatal("missing crun path accepted")
	}
}

func TestLinuxCommandRespectsBootstrapBudget(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	budget, err := NewBudget(10*time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	clock.Sleep(2 * time.Second)
	runner := &fakeRunner{}
	result := runLinuxCommand(
		context.Background(),
		CommandSpec{Path: "podman", Args: []string{"info"}, Timeout: 20 * time.Second},
		Dependencies{Runner: runner, Budget: budget},
		3*time.Second,
	)
	if !result.Success() {
		t.Fatalf("budgeted command failed: %+v", result)
	}
	if got := runner.calls[0].timeout; got != 5*time.Second {
		t.Fatalf("command timeout = %s, want 5s after 3s reserve", got)
	}
}

func TestLinuxRootlessSetupUsesPinnedPreflightWithoutSystemd(t *testing.T) {
	fixture := testSetup(t, ModeRootless)
	cfg, runner, files, clock := fixture.cfg, fixture.runner, fixture.files, fixture.clock
	runner.fn = func(c recordedCommand) CommandResult {
		if strings.Contains(strings.Join(c.args, " "), "OCIRuntime.Path") {
			return successResult(cfg.CrunPath + "\n")
		}
		return successResult("")
	}
	if err := platformSetup(context.Background(), testDeps(runner, files, clock), cfg); err != nil {
		t.Fatal(err)
	}
	if commandHas(
		runner,
		func(c recordedCommand) bool { return strings.Contains(strings.Join(c.args, " "), "systemctl") },
	) {
		t.Fatal("rootless setup invoked systemd")
	}
	if !commandHas(
		runner,
		func(c recordedCommand) bool { return strings.Contains(strings.Join(c.args, " "), busyboxImage) },
	) {
		t.Fatal("pinned BusyBox preflight missing")
	}
	if len(files.appends) != 0 {
		t.Fatal("rootless setup exported the rootful socket")
	}
}

func TestLinuxRootfulSetupOrderingAndEnvironment(t *testing.T) {
	fixture := testSetup(t, ModeRootful)
	cfg, runner, files, clock := fixture.cfg, fixture.runner, fixture.files, fixture.clock
	var serviceContents string
	runner.fn = func(c recordedCommand) CommandResult {
		if len(c.args) == 5 && slices.Equal(c.args[:3], []string{"install", "-m", "0644"}) {
			contents, err := os.ReadFile(c.args[3])
			if err != nil {
				t.Fatalf("read temporary service file: %v", err)
			}
			serviceContents = string(contents)
		}
		if strings.Contains(strings.Join(c.args, " "), "OCIRuntime.Path") {
			return successResult(cfg.CrunPath)
		}
		return successResult("")
	}
	if err := platformSetup(context.Background(), testDeps(runner, files, clock), cfg); err != nil {
		t.Fatal(err)
	}
	wantService := "[Service]\nExecStart=\nExecStart=" + cfg.PodmanPath + " --log-level=info system service --time=0\n"
	if serviceContents != wantService {
		t.Fatalf("systemd service contents = %q, want %q", serviceContents, wantService)
	}
	reloadIndex := commandIndex(runner, "daemon-reload")
	if reloadIndex < 0 || commandIndex(runner, "enable --now podman.socket") <= reloadIndex {
		t.Fatalf("systemd ordering wrong: %+v", runner.calls)
	}
	if !hasCommandContaining(runner, "--remote --url "+rootfulSocket) {
		t.Fatal("rootful API was not probed remotely")
	}
	if got := files.appends[cfg.GitHubEnvPath].String(); got != "DOCKER_HOST="+rootfulSocket+"\n" {
		t.Fatalf("GITHUB_ENV = %q", got)
	}
	if !hasCommandContaining(runner, busyboxImage) {
		t.Fatal("rootful BusyBox preflight missing")
	}
}

func TestLinuxRootfulReadinessFailureCollectsDiagnostics(t *testing.T) {
	fixture := testSetup(t, ModeRootful)
	cfg, runner, files, clock := fixture.cfg, fixture.runner, fixture.files, fixture.clock
	runner.fn = func(c recordedCommand) CommandResult {
		joined := strings.Join(c.args, " ")
		if strings.Contains(joined, "OCIRuntime.Path") {
			return successResult(cfg.CrunPath)
		}
		if strings.Contains(joined, " info") {
			return failureResult("unavailable")
		}
		return successResult("")
	}
	if err := platformSetup(context.Background(), testDeps(runner, files, clock), cfg); err == nil {
		t.Fatal("expected readiness failure")
	}
	for _, want := range []string{
		"status podman.socket",
		"status podman.service",
		"journalctl -u podman.socket",
		"journalctl -u podman.service",
	} {
		if !commandHas(
			runner,
			func(c recordedCommand) bool { return strings.Contains(strings.Join(c.args, " "), want) },
		) {
			t.Errorf("missing diagnostic %q", want)
		}
	}
}

func commandIndex(r *fakeRunner, text string) int {
	for i, c := range r.calls {
		if strings.Contains(strings.Join(c.args, " "), text) {
			return i
		}
	}
	return -1
}

func hasCommandContaining(r *fakeRunner, text string) bool {
	return commandHas(
		r,
		func(c recordedCommand) bool { return strings.Contains(strings.Join(c.args, " "), text) },
	)
}
