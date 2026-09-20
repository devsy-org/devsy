package workspacejournal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/status"
)

func TestJournalPersistsRedactedOperationAndPermissions(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC)
	secret := "journal-super-secret"
	journal, err := New(Options{
		Dir: dir,
		Now: func() time.Time { return now },
		Env: []string{"TOKEN=" + secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	journal.Reporter("demo").Report(status.Event{
		Pipeline:    status.PipelineWorkspaceUp,
		OperationID: "op-1",
		Phase:       status.PhaseReady,
		Step:        "using " + secret,
		State:       status.StateFailed,
		Duration:    1500 * time.Millisecond,
		Error: &status.ErrorInfo{
			Code: "boom", Message: "failed " + secret, Hint: "remove " + secret,
			Context: map[string]string{"detail": secret},
		},
	})
	e := onlyEvent(t, dir, "demo")
	if e.DurationMillis != 1500 || e.Error == nil || e.Error.Code != "boom" {
		t.Fatalf("event=%+v", e)
	}
	// #nosec G304 -- The test owns its temporary directory.
	data, err := os.ReadFile(filepath.Join(dir, "events-000001.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("secret persisted")
	}
	info, _ := os.Stat(filepath.Join(dir, "events-000001.ndjson"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestJournalRotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	journal, err := New(Options{Dir: dir, MaxBytes: 700, MaxSegmentBytes: 220})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		journal.Reporter("demo").Report(status.Event{
			Pipeline: status.PipelineWorkspaceUp,
			Phase:    status.PhaseReady,
			Step:     strings.Repeat("x", 50),
			State:    status.StateSucceeded,
		})
	}
	paths, err := segmentPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 2 {
		t.Fatalf("segments=%d", len(paths))
	}
	var total int64
	for _, p := range paths {
		info, _ := os.Stat(p)
		total += info.Size()
	}
	if total > 900 {
		t.Fatalf("journal unbounded: %d", total)
	}
}

func TestAppendSkipsOversizedEvents(t *testing.T) {
	dir := t.TempDir()
	journal, err := New(Options{Dir: dir, MaxBytes: 1000, MaxSegmentBytes: 300})
	if err != nil {
		t.Fatal(err)
	}
	journal.Reporter("demo").Report(status.Event{
		Pipeline: status.PipelineWorkspaceUp,
		Phase:    status.PhaseReady,
		Step:     strings.Repeat("x", 500),
		State:    status.StateSucceeded,
	})
	journal.Reporter("demo").Report(status.Event{
		Pipeline: status.PipelineWorkspaceUp,
		Phase:    status.PhaseReady,
		Step:     "small",
		State:    status.StateSucceeded,
	})
	events, err := Read(dir, "demo", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Step != "small" {
		t.Fatalf("events=%+v", events)
	}
	paths, err := segmentPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		info, _ := os.Stat(p)
		if info.Size() > 300 {
			t.Fatalf("segment %s exceeds limit: %d", p, info.Size())
		}
	}
}

func TestReadSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	contents := "not-json\n" +
		`{"schemaVersion":1,"timestamp":"2026-09-20T06:00:00Z",` +
		`"workspaceId":"demo","pipeline":"workspace_up","phase":"ready","state":"succeeded"}` +
		"\n{\"broken\":"
	path := filepath.Join(dir, "events-000001.ndjson")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	events, err := Read(dir, "demo", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
}

func TestReporterNeverPropagatesWriteFailure(t *testing.T) {
	journal, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	journal.dir = "/missing/parent/journal"
	journal.Reporter("demo").Report(status.Event{
		Phase: status.PhaseReady,
		State: status.StateSucceeded,
	})
	if !errors.Is(journal.Append("demo", status.Event{}), os.ErrNotExist) {
		t.Fatal("expected direct append failure")
	}
}

func onlyEvent(t *testing.T, dir, workspaceID string) Event {
	t.Helper()
	events, err := Read(dir, workspaceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	return events[0]
}

func TestJournalRotatesFromHighestExistingSegment(t *testing.T) {
	dir := t.TempDir()
	full := strings.Repeat("x", 100)
	for _, name := range []string{"events-000002.ndjson", "events-000003.ndjson"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(full+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	journal, err := New(Options{Dir: dir, MaxSegmentBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(
		"demo",
		status.Event{Phase: status.PhaseReady, State: status.StateSucceeded},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events-000004.ndjson")); err != nil {
		t.Fatalf("expected events-000004.ndjson: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "events-000003.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(full)+1) {
		t.Fatalf("existing segment grew to %d bytes", info.Size())
	}
}

func TestReadSkipsOversizedRecords(t *testing.T) {
	dir := t.TempDir()
	record := `{"schemaVersion":1,"timestamp":"2026-09-20T06:00:00Z",` +
		`"workspaceId":"demo","pipeline":"workspace_up","phase":"ready","state":"succeeded"}`
	oversized := `{"schemaVersion":1,"timestamp":"2026-09-20T06:00:01Z",` +
		`"workspaceId":"demo","pipeline":"workspace_up","phase":"ready","state":"succeeded","step":"` +
		strings.Repeat("x", 128*1024) + `"}`
	contents := record + "\n" + oversized + "\n" + record + "\n"
	if err := os.WriteFile(
		filepath.Join(dir, "events-000001.ndjson"),
		[]byte(contents),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	events, err := Read(dir, "demo", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%d", len(events))
	}
}

func TestReadSkipsIncompleteRecords(t *testing.T) {
	dir := t.TempDir()
	record := func(fields ...string) string {
		return "{" + strings.Join(fields, ",") + "}\n"
	}
	sv := `"schemaVersion":1`
	ts := `"timestamp":"2026-09-20T06:00:00Z"`
	ws := `"workspaceId":"demo"`
	pl := `"pipeline":"workspace_up"`
	ph := `"phase":"ready"`
	st := `"state":"succeeded"`
	contents := record(sv, ts, ws, pl, ph, st) +
		record(sv, ws, pl, ph, st) +
		record(sv, ts, ws, ph, st) +
		record(sv, ts, ws, pl, st) +
		record(sv, ts, ws, pl, ph)
	if err := os.WriteFile(
		filepath.Join(dir, "events-000001.ndjson"),
		[]byte(contents),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	events, err := Read(dir, "demo", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
}
