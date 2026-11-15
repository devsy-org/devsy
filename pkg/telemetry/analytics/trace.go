package analytics

import (
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/posthog/posthog-go"
)

// Phases of one upload, in the order the HTTP transport reaches them.
const (
	phaseDNS      = "dns"
	phaseConnect  = "connect"
	phaseTLS      = "tls"
	phaseSend     = "send"
	phaseResponse = "response"
)

var phaseOrder = []string{phaseDNS, phaseConnect, phaseTLS, phaseSend, phaseResponse}

// phaseTrace records where upload requests spend time, so a flush that hits
// its deadline can say which step was slow. Each request owns its own attempt,
// because the SDK uploads batches concurrently; the trace only publishes the
// latest one for the summary.
type phaseTrace struct {
	mu       sync.Mutex
	attempts int
	latest   *attempt
}

func (p *phaseTrace) newAttempt() *attempt {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	p.latest = &attempt{number: p.attempts, open: map[string]openPhase{}}
	return p.latest
}

// summary describes the latest upload attempt. It is safe to call on a nil
// receiver and while requests are still running.
func (p *phaseTrace) summary() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	latest := p.latest
	p.mu.Unlock()

	if latest == nil {
		return "no upload started"
	}
	return latest.summary()
}

// attempt is the phase state of a single request.
type attempt struct {
	mu     sync.Mutex
	number int
	open   map[string]openPhase
	done   []donePhase
}

type openPhase struct {
	start time.Time
	count int
	err   error
}

type donePhase struct {
	name     string
	duration time.Duration
	err      error
}

func (a *attempt) begin(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.open[name]
	if entry.count == 0 {
		entry.start = time.Now()
	}
	entry.count++
	a.open[name] = entry
}

// end closes a phase. Dials can overlap (dual-stack addresses), so a failed
// one only closes the phase after every other dial has finished. The first
// success closes it at once and later results are ignored.
func (a *attempt) end(name string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.open[name]
	if !ok {
		return
	}
	entry.count--
	if err != nil && entry.err == nil {
		entry.err = err
	}
	if err != nil && entry.count > 0 {
		a.open[name] = entry
		return
	}
	if err == nil {
		entry.err = nil
	}
	a.closeLocked(name, entry)
}

// fail closes every phase still running when the request ends in error. The
// SDK cancels the request at the flush deadline, so this keeps the phase that
// was in flight visible after the cancellation.
func (a *attempt) fail(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, name := range phaseOrder {
		if entry, ok := a.open[name]; ok {
			entry.err = err
			a.closeLocked(name, entry)
		}
	}
}

func (a *attempt) closeLocked(name string, entry openPhase) {
	delete(a.open, name)
	a.done = append(a.done, donePhase{
		name: name, duration: time.Since(entry.start), err: entry.err,
	})
}

func (a *attempt) summary() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	var parts []string
	for _, d := range a.done {
		part := d.name + " " + d.duration.Round(time.Millisecond).String()
		if d.err != nil {
			part += " (" + d.err.Error() + ")"
		}
		parts = append(parts, part)
	}
	now := time.Now()
	for _, name := range phaseOrder {
		if entry, ok := a.open[name]; ok {
			parts = append(parts, "stuck in "+name+" for "+
				now.Sub(entry.start).Round(time.Millisecond).String())
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no phase reached")
	}
	return "upload attempt " + strconv.Itoa(a.number) + ": " + strings.Join(parts, ", ")
}

func (a *attempt) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { a.begin(phaseDNS) },
		DNSDone:           func(info httptrace.DNSDoneInfo) { a.end(phaseDNS, info.Err) },
		ConnectStart:      func(_, _ string) { a.begin(phaseConnect) },
		ConnectDone:       func(_, _ string, err error) { a.end(phaseConnect, err) },
		TLSHandshakeStart: func() { a.begin(phaseTLS) },
		TLSHandshakeDone:  func(_ tls.ConnectionState, err error) { a.end(phaseTLS, err) },
		// A reused connection skips DNS, connect and TLS and lands here.
		GotConn: func(httptrace.GotConnInfo) { a.begin(phaseSend) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			a.end(phaseSend, info.Err)
			if info.Err == nil {
				a.begin(phaseResponse)
			}
		},
		// The response phase stays open past the first byte: a server that
		// sends part of the headers and stalls is still a stuck response.
		// RoundTrip closes it.
	}
}

// tracingTransport reports phase timings of each request to a phaseTrace.
type tracingTransport struct {
	base  http.RoundTripper
	trace *phaseTrace
}

func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	a := t.trace.newAttempt()
	ctx := httptrace.WithClientTrace(req.Context(), a.clientTrace())
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		a.fail(err)
	} else {
		a.end(phaseResponse, nil)
	}
	return resp, err
}

// newTracingTransport mirrors the SDK's default transport, which a custom
// Config.Transport replaces.
func newTracingTransport() (http.RoundTripper, *phaseTrace) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.MaxIdleConns = posthog.DefaultIdleConns
	base.MaxIdleConnsPerHost = posthog.DefaultIdleConnsPerHost
	trace := &phaseTrace{}
	return &tracingTransport{base: base, trace: trace}, trace
}
