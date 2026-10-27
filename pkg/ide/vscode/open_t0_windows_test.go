//go:build windows

package vscode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	devsylog "github.com/devsy-org/devsy/pkg/log"
	"go.uber.org/zap/zapcore"
)

func TestT0RealVSCodiumCLIToURIFallback(t *testing.T) {
	cliPath := os.Getenv("DEVSY_T0_EDITOR_CLI")
	workspace := os.Getenv("DEVSY_T0_WORKSPACE")
	folder := os.Getenv("DEVSY_T0_FOLDER")
	if cliPath == "" || workspace == "" || folder == "" {
		t.Fatal("native VSCodium CLI, workspace, and folder are required")
	}
	selectedPath, err := exec.LookPath("codium")
	if err != nil {
		t.Fatal("VSCodium CLI is not selected by PATH")
	}
	if !strings.EqualFold(filepath.Base(selectedPath), "codium.cmd") ||
		!strings.EqualFold(filepath.Clean(selectedPath), filepath.Clean(cliPath)) {
		t.Fatal("selected CLI is not the pinned VSCodium codium.cmd wrapper")
	}

	version := firstNonemptyLine(runT0VSCodiumPowerShell(t, "& $env:DEVSY_T0_EDITOR_CLI --version"))
	if version == "" {
		t.Fatal("VSCodium CLI returned no version")
	}
	extensions := runT0VSCodiumPowerShell(
		t,
		"& $env:DEVSY_T0_EDITOR_CLI --list-extensions --show-versions",
	)
	sshExtension := ""
	for _, extension := range strings.Split(strings.ReplaceAll(extensions, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(
			strings.ToLower(strings.TrimSpace(extension)),
			"jeanp413.open-remote-ssh@",
		) {
			sshExtension = strings.TrimSpace(extension)
			break
		}
	}
	if sshExtension == "" {
		t.Fatal("Open Remote SSH extension was not enumerated by the real VSCodium CLI")
	}

	logs := devsylog.InitTestObserved(t, zapcore.DebugLevel)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	params := OpenParams{
		Workspace: workspace,
		Folder:    folder,
		NewWindow: false,
		Flavor:    FlavorCodium,
	}
	if err := Open(ctx, params); err != nil {
		t.Fatalf("VSCodium launch failed (error type %T)", err)
	}

	route := ""
	afterCLIFailure := false
	for _, entry := range logs.All() {
		if entry.Message == "launch request dispatched" {
			fields := entry.ContextMap()
			route, _ = fields["route"].(string)
			afterCLIFailure, _ = fields["after_cli_failure"].(bool)
		}
	}
	if route != routeCLI && route != routeURI {
		t.Fatalf("VSCodium launch emitted no recognized dispatch route")
	}
	t.Logf(
		"VSCodiumVersion=%s selectedCLI=codium.cmd SSHExtension=%s dispatchRoute=%s afterCLIFailure=%t",
		version,
		sshExtension,
		route,
		afterCLIFailure,
	)
}

func runT0VSCodiumPowerShell(t *testing.T, command string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).
		CombinedOutput()
	if err != nil {
		t.Fatalf("VSCodium CLI probe failed (error type %T)", err)
	}
	return string(output)
}

func firstNonemptyLine(output string) string {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
