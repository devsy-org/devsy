package ci

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/status"
)

func TestStatusReporterUsesDeterministicASCIIOutput(t *testing.T) {
	var out bytes.Buffer
	reporter, err := newStatusReporter("plain", &out, false, false)
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

func TestStatusReporterNoStatus(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		var out bytes.Buffer
		reporter, err := newStatusReporter("plain", &out, verbose, true)
		if err != nil {
			t.Fatal(err)
		}
		reporter.Report(status.Event{Phase: status.PhaseRunningCommand, State: status.StateStarted})
		if out.Len() != 0 {
			t.Fatalf("unexpected status: %q", out.String())
		}
	}
}

func TestStatusReporterJSON(t *testing.T) {
	var out bytes.Buffer
	reporter, err := newStatusReporter("json", &out, false, false)
	if err != nil {
		t.Fatal(err)
	}
	reporter.Report(status.Event{Phase: status.PhaseRunningCommand, State: status.StateStarted})
	var event map[string]any
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatalf("invalid JSON status %q: %v", out.String(), err)
	}
	if event["kind"] != "status" || event["phase"] != string(status.PhaseRunningCommand) ||
		event["state"] != string(status.StateStarted) {
		t.Fatalf("unexpected status event: %#v", event)
	}
}
