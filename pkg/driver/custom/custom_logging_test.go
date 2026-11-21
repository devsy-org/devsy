package custom

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

const (
	testWindows        = "windows"
	testShell          = "/bin/sh"
	testStartContainer = "startDevContainer"
)

func TestCustomDiagnosticsRedactAndPreserveExit(t *testing.T) {
	if runtime.GOOS == testWindows {
		t.Skip("requires /bin/sh")
	}
	observed := log.InitTestObserved(t, slog.LevelDebug)
	c := &customDriver{workspaceInfo: &provider.AgentWorkspaceInfo{
		Origin:    t.TempDir(),
		Workspace: &provider.Workspace{Provider: provider.WorkspaceProviderConfig{Name: "example"}},
		Options:   map[string]config.OptionValue{"custom_value": {Value: "private-canary-value"}},
	}}
	err := c.runCommand(context.Background(), runCommandOptions{
		workspaceId: "workspace-test",
		name:        testStartContainer,
		command: types.StrArray{
			testShell,
			"-c",
			`printf '%s\n' '{"level":"error","msg":"private-canary-value"}' >&2; exit 7`,
		},
		diagnosticStderr: true,
	})
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("expected exit 7, got %v", err)
	}
	if strings.Contains(err.Error(), "private-canary-value") {
		t.Fatalf("secret in error: %s", err)
	}
	entries := observed.FilterMessage("***").All()
	if len(entries) != 1 || entries[0].AttrsMap()["provider"] != "example" ||
		entries[0].AttrsMap()["workspace_id"] != "workspace-test" {
		t.Fatalf("unexpected records: %#v", entries)
	}
}

func TestCustomCommandPreservesRawUserStreams(t *testing.T) {
	if runtime.GOOS == testWindows {
		t.Skip("requires /bin/sh")
	}
	c := &customDriver{workspaceInfo: &provider.AgentWorkspaceInfo{Origin: t.TempDir()}}
	var stdout, stderr bytes.Buffer
	err := c.runCommand(context.Background(), runCommandOptions{
		name: "commandDevContainer",
		command: types.StrArray{
			testShell,
			"-c",
			`printf '\001{"level":"info"}'; printf 'ordinary stderr' >&2`,
		},
		stdout:    &stdout,
		stderr:    &stderr,
		rawStdout: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "\x01{\"level\":\"info\"}" || stderr.String() != "ordinary stderr" {
		t.Fatalf("unexpected streams: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCustomDiagnosticsBoundedAndDrained(t *testing.T) {
	if runtime.GOOS == testWindows {
		t.Skip("requires /bin/sh")
	}
	log.InitTestObserved(t, slog.LevelDebug)
	c := &customDriver{workspaceInfo: &provider.AgentWorkspaceInfo{Origin: t.TempDir()}}
	err := c.runCommand(context.Background(), runCommandOptions{
		name: testStartContainer,
		command: types.StrArray{testShell, "-c", `printf 'old-marker\n' >&2; i=0; ` +
			`while [ "$i" -lt 60 ]; do printf '%0700d\n' 0 >&2; i=$((i+1)); done; ` +
			`printf 'last-marker' >&2; exit 7`},
		diagnosticStderr: true,
	})
	if err == nil || len(err.Error()) > 32*1024+256 ||
		strings.Contains(err.Error(), "old-marker") ||
		!strings.Contains(err.Error(), "last-marker") {
		t.Fatalf("unexpected diagnostic tail: %v", err)
	}
}

func TestCustomDiagnosticsPreserveCancellation(t *testing.T) {
	if runtime.GOOS == testWindows {
		t.Skip("requires /bin/sh")
	}
	log.InitTestObserved(t, slog.LevelDebug)
	c := &customDriver{workspaceInfo: &provider.AgentWorkspaceInfo{Origin: t.TempDir()}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.runCommand(ctx, runCommandOptions{
		name:             testStartContainer,
		command:          types.StrArray{testShell, "-c", "exit 0"},
		diagnosticStdout: true,
		diagnosticStderr: true,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestCustomFailureTailExcludesPresentationStdout(t *testing.T) {
	if runtime.GOOS == testWindows {
		t.Skip("requires /bin/sh")
	}
	c := &customDriver{workspaceInfo: &provider.AgentWorkspaceInfo{Origin: t.TempDir()}}
	err := c.runCommand(context.Background(), runCommandOptions{
		command: types.StrArray{
			testShell, "-c", "printf 'stdout-canary\\n'; printf 'stderr-canary\\n' >&2; exit 7",
		},
		diagnosticStdout: true,
		diagnosticStderr: true,
	})
	if err == nil || !strings.Contains(err.Error(), "stderr-canary") ||
		strings.Contains(err.Error(), "stdout-canary") {
		t.Fatalf("unexpected diagnostic failure: %v", err)
	}
}
