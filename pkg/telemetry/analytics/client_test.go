package analytics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/posthog/posthog-go"
)

const (
	eventGroup = "event"
	userGroup  = "user"
)

func TestBuildProperties_MergesEventAndUserWithoutCollision(t *testing.T) {
	event := Event{
		eventGroup: {
			KeyType:      "devsy_workspace_count",
			KeyMachineID: "m",
			KeyTimestamp: int64(1),
			"count":      7,
			"version":    "1.2.3",
		},
		userGroup: {
			KeyMachineID: "m",
			KeyTimestamp: int64(1),
			"os_name":    "darwin",
			"os_arch":    "arm64",
		},
	}

	got := buildProperties(event)

	want := map[string]any{
		"count":   7,
		"version": "1.2.3",
		"os_name": "darwin",
		"os_arch": "arm64",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("property %q = %v, want %v", k, got[k], v)
		}
	}
}

func TestBuildProperties_ExcludesReservedKeys(t *testing.T) {
	event := Event{
		eventGroup: {
			KeyType:      "devsy_cli",
			KeyMachineID: "m",
			KeyTimestamp: int64(1),
			"command":    "up",
		},
		userGroup: {
			KeyMachineID: "m",
			KeyTimestamp: int64(1),
		},
	}

	got := buildProperties(event)

	for _, reserved := range []string{KeyType, KeyMachineID, KeyTimestamp} {
		if _, ok := got[reserved]; ok {
			t.Errorf("reserved key %q should not appear in properties", reserved)
		}
	}
	if got["command"] != "up" {
		t.Errorf("command = %v, want up", got["command"])
	}
}

func newTestClient(t *testing.T, endpoint string, timeout time.Duration) *client {
	t.Helper()
	ph, err := posthog.NewWithConfig("test-key", posthogConfig(endpoint))
	if err != nil {
		t.Fatal(err)
	}
	return &client{phClient: ph, flushTimeout: timeout}
}

func testEvent() Event {
	return Event{
		eventGroup: {KeyType: "devsy_cli", KeyMachineID: "machine", "command": "up"},
		userGroup:  {KeyMachineID: "machine"},
	}
}

func TestFlush_DeliversQueuedEvents(t *testing.T) {
	var batches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		batches.Add(1)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv.URL, 5*time.Second)
	c.RecordEvent(testEvent())
	c.Flush()

	if got := batches.Load(); got != 1 {
		t.Fatalf("batches delivered = %d, want 1", got)
	}
}

func TestFlush_CancelsHungUploadAtDeadline(t *testing.T) {
	cancelled := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// The server only notices a closed connection after the body is read.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
		}
	}))
	// Cleanups run last in, first out: release the handler before Close waits on it.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	const timeout = 200 * time.Millisecond
	c := newTestClient(t, srv.URL, timeout)
	c.RecordEvent(testEvent())

	start := time.Now()
	c.Flush()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Flush took %s with a %s timeout", elapsed, timeout)
	}

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight upload was not cancelled after the deadline")
	}
}

func TestFlush_SecondCallIsNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv.URL, time.Second)
	c.RecordEvent(testEvent())
	c.Flush()

	start := time.Now()
	c.Flush()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("second Flush took %s", elapsed)
	}
}
