package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHServerCommand_EscapesAgentPath(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	malicious := "/tmp/devsy'; touch " + marker + "; echo 'still-quoted"

	command := sshServerCommand(malicious, false)

	// The built command names a nonexistent binary, so it's expected to
	// fail; only the absence of the marker file matters here.
	_ = exec.Command("sh", "-c", command).Run() // #nosec G204 -- injection payload under test

	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("agent path broke out of shell quoting and ran injected command: %s", command)
	}
	if !strings.Contains(command, "internal") || !strings.Contains(command, "ssh-server") {
		t.Fatalf("command missing expected subcommand: %s", command)
	}
}

func TestSSHServerCommand_AppendsDebugFlag(t *testing.T) {
	command := sshServerCommand("/usr/local/bin/devsy", true)

	if !strings.HasSuffix(command, "--debug") {
		t.Fatalf("expected trailing --debug flag, got: %s", command)
	}
}

func TestSSHServerCommand_OmitsDebugFlagWhenDisabled(t *testing.T) {
	command := sshServerCommand("/usr/local/bin/devsy", false)

	if strings.Contains(command, "--debug") {
		t.Fatalf("did not expect --debug flag, got: %s", command)
	}
}
