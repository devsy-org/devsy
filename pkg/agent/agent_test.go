package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
)

func TestSSHServerCommand_EscapesAgentPath(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	malicious := "/tmp/devsy'; touch " + marker + "; echo 'still-quoted"

	command := sshServerCommand(malicious)

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

func TestSSHServerCommandForwardsEffectiveLevel(t *testing.T) {
	for _, tc := range []struct {
		level log.Level
		want  string
	}{
		{log.SlogLevelWarn, "warn"},
		{log.SlogLevelInfo, "info"},
		{log.SlogLevelTrace, "trace"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			log.InitTestObserved(t, tc.level)
			command := sshServerCommand("/usr/local/bin/devsy")
			if !strings.Contains(command, "--log-level "+tc.want) {
				t.Fatalf("effective level missing from %q", command)
			}
		})
	}
}
