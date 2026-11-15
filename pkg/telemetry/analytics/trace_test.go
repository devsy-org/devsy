package analytics

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"regexp"
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
	a := trace.newAttempt()
	a.begin(phaseDNS)
	a.end(phaseDNS, nil)
	a.begin(phaseConnect)

	got := trace.summary()
	for _, want := range []string{"upload attempt 1", "dns ", "stuck in connect"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q lacks %q", got, want)
		}
	}

	a.fail(errors.New("context canceled"))
	got = trace.summary()
	if !strings.Contains(got, "connect") || !strings.Contains(got, "(context canceled)") {
		t.Errorf("summary after failure = %q", got)
	}
	if strings.Contains(got, "stuck in") {
		t.Errorf("failed phase still reported as stuck: %q", got)
	}
}

func TestPhaseTraceSummary_ReportsLatestAttempt(t *testing.T) {
	trace := &phaseTrace{}
	first := trace.newAttempt()
	first.begin(phaseTLS)
	trace.newAttempt()

	got := trace.summary()
	if !strings.Contains(got, "upload attempt 2") || strings.Contains(got, "tls") {
		t.Fatalf("summary = %q", got)
	}
}

func TestAttempts_DoNotShareState(t *testing.T) {
	trace := &phaseTrace{}
	first := trace.newAttempt()
	first.begin(phaseResponse)
	second := trace.newAttempt()
	second.begin(phaseResponse)

	// Finishing the first request must not close the second one's wait.
	first.end(phaseResponse, nil)

	got := trace.summary()
	if !strings.Contains(got, "upload attempt 2") || !strings.Contains(got, "stuck in response") {
		t.Fatalf("summary = %q, want the second attempt still waiting", got)
	}
}

func TestAttempt_FailedDialKeepsPhaseOpenWhileAnotherIsPending(t *testing.T) {
	trace := &phaseTrace{}
	a := trace.newAttempt()
	a.begin(phaseConnect)
	a.begin(phaseConnect)

	a.end(phaseConnect, errors.New("network unreachable"))
	if got := trace.summary(); !strings.Contains(got, "stuck in connect") {
		t.Fatalf("summary = %q, want connect still open for the pending dial", got)
	}

	a.fail(errors.New("context canceled"))
	if got := trace.summary(); !strings.Contains(got, "(context canceled)") {
		t.Fatalf("summary = %q, want the cancellation on the pending dial", got)
	}
}

func TestAttempt_FirstSuccessfulDialClosesPhase(t *testing.T) {
	trace := &phaseTrace{}
	a := trace.newAttempt()
	a.begin(phaseConnect)
	a.begin(phaseConnect)

	a.end(phaseConnect, nil)
	// The losing dial reports a cancellation after the winner connected.
	a.end(phaseConnect, errors.New("operation was canceled"))

	got := trace.summary()
	if strings.Contains(got, "stuck in") || strings.Contains(got, "canceled") {
		t.Fatalf("summary = %q, want a clean connect", got)
	}
}

func TestAttempt_FailedWriteIsAttributedToSend(t *testing.T) {
	trace := &phaseTrace{}
	a := trace.newAttempt()
	a.clientTrace().GotConn(httptrace.GotConnInfo{})
	a.clientTrace().WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("broken pipe")})

	got := trace.summary()
	if !strings.Contains(got, "send ") || !strings.Contains(got, "(broken pipe)") {
		t.Fatalf("summary = %q, want the write failure on send", got)
	}
	if strings.Contains(got, "response") {
		t.Fatalf("summary = %q, response must not start after a failed write", got)
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

// stallingServer answers each request with reply and then goes silent. It
// returns the address to upload to.
func stallingServer(t *testing.T, reply string) string {
	t.Helper()
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
			go func() {
				buf := make([]byte, 4096)
				_, _ = conn.Read(buf)
				_, _ = conn.Write([]byte(reply))
			}()
		}
	}()
	return "http://" + listener.Addr().String()
}

func TestFlushTimeout_ReportsResponseStalls(t *testing.T) {
	tests := map[string]string{
		// Part of the status line, then silence.
		"stalled after first byte": "HTTP/1.1 2",
		// Complete headers, then a body that never finishes. The SDK reads
		// the body of non-2xx replies, so this stalls after RoundTrip returns.
		"stalled body": "HTTP/1.1 500 Internal Server Error\r\nContent-Length: 100\r\n\r\npartial",
	}
	for name, reply := range tests {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, stallingServer(t, reply), 300*time.Millisecond)
			c.RecordEvent(testEvent())
			c.Flush()

			// Either still waiting or interrupted with an error, never a clean finish.
			got := c.trace.summary()
			if !stalledResponse.MatchString(got) {
				t.Fatalf("summary = %q, want the stall reported in the response phase", got)
			}
		})
	}
}

var stalledResponse = regexp.MustCompile(`stuck in response|response \S+ \(`)

func TestFlush_SuccessfulUploadLeavesNoOpenPhase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv.URL, 5*time.Second)
	c.RecordEvent(testEvent())
	c.Flush()

	got := c.trace.summary()
	if strings.Contains(got, "stuck in") || strings.Contains(got, "(") {
		t.Fatalf("summary = %q, want every phase completed cleanly", got)
	}
}
