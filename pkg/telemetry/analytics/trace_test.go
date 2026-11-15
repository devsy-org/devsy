package analytics

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPhaseTraceSummary_NoUpload(t *testing.T) {
	if got := (&phaseTrace{}).summary(); got != "no upload started" {
		t.Fatalf("summary = %q", got)
	}
	var nilTrace *phaseTrace
	if got := nilTrace.summary(); got != "" {
		t.Fatalf("nil summary = %q", got)
	}
}

func TestPhaseTraceSummary_ReportsOpenAndFailedPhases(t *testing.T) {
	trace := &phaseTrace{}
	trace.newAttempt()
	trace.begin(phaseDNS)
	trace.end(phaseDNS, nil)
	trace.begin(phaseConnect)

	got := trace.summary()
	for _, want := range []string{"upload attempt 1", "dns ", "stuck in connect"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q lacks %q", got, want)
		}
	}

	trace.fail(errors.New("context canceled"))
	got = trace.summary()
	if !strings.Contains(got, "connect") || !strings.Contains(got, "(context canceled)") {
		t.Errorf("summary after failure = %q", got)
	}
	if strings.Contains(got, "stuck in") {
		t.Errorf("failed phase still reported as stuck: %q", got)
	}
}

func TestPhaseTraceSummary_RestartsOnNewAttempt(t *testing.T) {
	trace := &phaseTrace{}
	trace.newAttempt()
	trace.begin(phaseTLS)
	trace.newAttempt()

	got := trace.summary()
	if !strings.Contains(got, "upload attempt 2") || strings.Contains(got, "tls") {
		t.Fatalf("summary = %q", got)
	}
}

func TestFlushTimeout_ReportsStuckTLSHandshake(t *testing.T) {
	// Accepts the connection but never answers the TLS ClientHello.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	c := newTestClient(t, "https://"+listener.Addr().String(), 300*time.Millisecond)
	c.RecordEvent(testEvent())
	c.Flush()

	got := c.trace.summary()
	// Whether the SDK's cancellation has reached the transport by now varies,
	// so the stuck phase reads either as "stuck in tls" or as an interrupted "tls".
	if !strings.Contains(got, "connect ") || !strings.Contains(got, "tls ") {
		t.Fatalf("summary = %q, want completed connect and a tls phase", got)
	}
}

func TestFlushTimeout_ReportsStuckResponse(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	c := newTestClient(t, srv.URL, 300*time.Millisecond)
	c.RecordEvent(testEvent())
	c.Flush()

	got := c.trace.summary()
	if !strings.Contains(got, "send ") || !strings.Contains(got, "response ") {
		t.Fatalf("summary = %q, want sent request and a response phase", got)
	}
}
