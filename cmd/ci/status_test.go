package ci

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/status"
)

func TestStatusReporterUsesDeterministicASCIIOutput(t *testing.T) {
	var out bytes.Buffer
	reporter, err := newStatusReporter(&out, false)
	if err != nil {
		t.Fatalf("newStatusReporter: %v", err)
	}

	reporter.Report(status.Event{Phase: status.PhaseRunningCommand, State: status.StateStarted})
	reporter.Report(status.Event{
		Phase:    status.PhaseRunningCommand,
		State:    status.StateSucceeded,
		Duration: 1250 * 1000 * 1000,
	})

	got := out.String()
	if !strings.Contains(got, "[RUN]  ci: running command") {
		t.Fatalf("started output = %q", got)
	}
	if !strings.Contains(got, "[OK]   ci: running command (1.2s)") {
		t.Fatalf("completed output = %q", got)
	}
	for _, r := range got {
		if r > 127 {
			t.Fatalf("output contains non-ASCII rune %q: %q", r, got)
		}
	}
}
