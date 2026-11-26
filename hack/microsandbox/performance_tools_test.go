package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const installCommand = "install"

type performanceToolsRunner struct {
	moduleJSON    string
	failList      bool
	failInstallAt int
	commands      [][]string
	environments  [][]string
}

func (runner *performanceToolsRunner) run(command *exec.Cmd) error {
	runner.commands = append(runner.commands, append([]string(nil), command.Args[1:]...))
	runner.environments = append(runner.environments, append([]string(nil), command.Env...))
	if len(command.Args) > 1 && command.Args[1] == "list" {
		if runner.failList {
			return errors.New("module query failed")
		}
		_, err := io.WriteString(command.Stdout, runner.moduleJSON)
		return err
	}
	installCount := countPerformanceToolInstalls(runner.commands)
	if runner.failInstallAt == installCount {
		return errors.New("tool build failed")
	}
	return nil
}

func TestBuildPerformanceToolsCommandRejectsInvalidInvocation(t *testing.T) {
	if code := runCLI(
		[]string{"build-performance-tools", "--output-dir", "relative"},
		io.Discard,
		io.Discard,
	); code != 2 {
		t.Fatalf("invalid tool build invocation exit = %d, want 2", code)
	}
}

func TestBuildPerformanceToolsCommandRequiresLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("command is available on Linux")
	}
	if code := buildPerformanceToolsCLI(nil, io.Discard, io.Discard); code != 2 {
		t.Fatalf("non-Linux build command exit = %d, want 2", code)
	}
}

func TestBuildPerformanceToolsRejectsUnpinnedModuleBeforeInstall(t *testing.T) {
	for _, test := range []struct {
		name       string
		moduleJSON string
	}{
		{
			name:       "missing version",
			moduleJSON: `{"Path":"github.com/devsy-org/devsy-runtime-sdk"}`,
		},
		{
			name:       "invalid version",
			moduleJSON: `{"Path":"github.com/devsy-org/devsy-runtime-sdk","Version":"vnot-semver"}`,
		},
		{
			name:       "incomplete semantic version",
			moduleJSON: `{"Path":"github.com/devsy-org/devsy-runtime-sdk","Version":"v1"}`,
		},
		{
			name: "replacement",
			moduleJSON: strings.Join([]string{
				`{"Path":"github.com/devsy-org/devsy-runtime-sdk",`,
				`"Version":"v1.6.0","Replace":{"Path":"../runtime-sdk"}}`,
			}, ""),
		},
		{
			name:       "different module",
			moduleJSON: `{"Path":"example.com/runtime-sdk","Version":"v1.6.0"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			runner := &performanceToolsRunner{moduleJSON: test.moduleJSON}
			err := buildPerformanceTools(context.Background(), dir, runner.run, commandOutput{})
			if err == nil {
				t.Fatal("expected SDK module validation error")
			}
			if got := countPerformanceToolInstalls(runner.commands); got != 0 {
				t.Fatalf("ran %d installs before rejecting module metadata", got)
			}
			assertNoSDKVersion(t, dir)
		})
	}
}

func TestBuildPerformanceToolsDoesNotWriteMetadataAfterFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			dir := t.TempDir()
			metadata := filepath.Join(dir, "sdk-version")
			if err := os.WriteFile(metadata, []byte("stale\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runner := &performanceToolsRunner{
				moduleJSON:    pinnedSDKModuleJSON,
				failInstallAt: failAt,
			}
			err := buildPerformanceTools(context.Background(), dir, runner.run, commandOutput{})
			if err == nil {
				t.Fatal("expected tool build error")
			}
			assertNoSDKVersion(t, dir)
		})
	}
}

func TestBuildPerformanceToolsUsesPinnedToolsAndWritesVersionLast(t *testing.T) {
	dir := t.TempDir()
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", oldPath)
	t.Setenv("GOBIN", "/old/bin")
	t.Setenv("CGO_ENABLED", "1")
	runner := &performanceToolsRunner{moduleJSON: pinnedSDKModuleJSON}
	if err := buildPerformanceTools(
		context.Background(),
		dir,
		runner.run,
		commandOutput{},
	); err != nil {
		t.Fatal(err)
	}
	assertPinnedToolInstalls(t, runner, dir, oldPath)
	version, err := os.ReadFile(filepath.Join(dir, "sdk-version"))
	if err != nil {
		t.Fatal(err)
	}
	if string(version) != "v1.6.0\n" {
		t.Fatalf("SDK version metadata = %q", version)
	}
}

func assertPinnedToolInstalls(t *testing.T, runner *performanceToolsRunner, dir, path string) {
	t.Helper()
	wantPackages := []string{
		"github.com/devsy-org/devsy-runtime-sdk/cmd/devsy-runtime-spawn-probe@v1.6.0",
		"github.com/devsy-org/devsy-runtime-sdk/cmd/devsy-runtime-supervisor@v1.6.0",
	}
	if got := countPerformanceToolInstalls(runner.commands); got != len(wantPackages) {
		t.Fatalf("install count = %d, want %d", got, len(wantPackages))
	}
	for index, want := range wantPackages {
		args := runner.commands[index+1]
		if len(args) != 3 || args[0] != installCommand || args[1] != "-trimpath" ||
			args[2] != want {
			t.Fatalf("install command = %#v, want go install -trimpath %s", args, want)
		}
		assertBuildEnvironment(t, runner.environments[index+1], dir, path)
	}
}

func TestBuildPerformanceToolsRejectsRelativeOutputDirectory(t *testing.T) {
	runner := &performanceToolsRunner{moduleJSON: pinnedSDKModuleJSON}
	err := buildPerformanceTools(context.Background(), "relative", runner.run, commandOutput{})
	if err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("relative output dir error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("ran commands with invalid output dir: %#v", runner.commands)
	}
}

const pinnedSDKModuleJSON = `{"Path":"github.com/devsy-org/devsy-runtime-sdk","Version":"v1.6.0"}`

func countPerformanceToolInstalls(commands [][]string) int {
	count := 0
	for _, args := range commands {
		if len(args) > 0 && args[0] == installCommand {
			count++
		}
	}
	return count
}

func assertBuildEnvironment(t *testing.T, environment []string, outputDir, path string) {
	t.Helper()
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		values[key] = value
	}
	if values["GOBIN"] != outputDir || values["CGO_ENABLED"] != "0" || values["PATH"] != path {
		t.Fatalf("unexpected build environment: %#v", values)
	}
}

func assertNoSDKVersion(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "sdk-version")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sdk-version metadata exists after failed build: %v", err)
	}
}
