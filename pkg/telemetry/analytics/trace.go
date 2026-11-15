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
// its deadline can say which step was slow.
type phaseTrace struct {
	mu       sync.Mutex
	attempts int
	open     map[string]openPhase
	done     []donePhase
}

type openPhase struct {
	start time.Time
	count int
}

type donePhase struct {
	name     string
	duration time.Duration
	err      error
}

func (p *phaseTrace) newAttempt() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	p.open = map[string]openPhase{}
	p.done = nil
}

func (p *phaseTrace) begin(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.open[name]
	if entry.count == 0 {
		entry.start = time.Now()
	}
	entry.count++
	p.open[name] = entry
}

func (p *phaseTrace) end(name string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endLocked(name, err)
}

func (p *phaseTrace) endLocked(name string, err error) {
	entry, ok := p.open[name]
	if !ok {
		return
	}
	entry.count--
	if entry.count > 0 && err == nil {
		p.open[name] = entry
		return
	}
	delete(p.open, name)
	p.done = append(p.done, donePhase{name: name, duration: time.Since(entry.start), err: err})
}

// fail closes every phase still running when a request ends in error. The SDK
// cancels the request at the flush deadline, so this keeps the phase that was
// in flight visible after the cancellation.
func (p *phaseTrace) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, name := range phaseOrder {
		p.endLocked(name, err)
	}
}

// summary describes the latest upload attempt. It is safe to call on a nil
// receiver and while requests are still running.
func (p *phaseTrace) summary() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.attempts == 0 {
		return "no upload started"
	}
	var parts []string
	for _, d := range p.done {
		part := d.name + " " + d.duration.Round(time.Millisecond).String()
		if d.err != nil {
			part += " (" + d.err.Error() + ")"
		}
		parts = append(parts, part)
	}
	now := time.Now()
	for _, name := range phaseOrder {
		if entry, ok := p.open[name]; ok {
			parts = append(parts, "stuck in "+name+" for "+
				now.Sub(entry.start).Round(time.Millisecond).String())
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no phase reached")
	}
	return "upload attempt " + strconv.Itoa(p.attempts) + ": " + strings.Join(parts, ", ")
}

func (p *phaseTrace) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { p.begin(phaseDNS) },
		DNSDone:           func(info httptrace.DNSDoneInfo) { p.end(phaseDNS, info.Err) },
		ConnectStart:      func(_, _ string) { p.begin(phaseConnect) },
		ConnectDone:       func(_, _ string, err error) { p.end(phaseConnect, err) },
		TLSHandshakeStart: func() { p.begin(phaseTLS) },
		TLSHandshakeDone:  func(_ tls.ConnectionState, err error) { p.end(phaseTLS, err) },
		// A reused connection skips DNS, connect and TLS and lands here.
		GotConn:              func(httptrace.GotConnInfo) { p.begin(phaseSend) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { p.end(phaseSend, nil); p.begin(phaseResponse) },
		GotFirstResponseByte: func() { p.end(phaseResponse, nil) },
	}
}

// tracingTransport reports phase timings of each request to a phaseTrace.
type tracingTransport struct {
	base  http.RoundTripper
	trace *phaseTrace
}

func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.trace.newAttempt()
	ctx := httptrace.WithClientTrace(req.Context(), t.trace.clientTrace())
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		t.trace.fail(err)
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
