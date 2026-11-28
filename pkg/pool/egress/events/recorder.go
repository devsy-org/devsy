// Package events turns the egress proxy's decisions into audit events.
package events

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/devsy-org/devsy-signals/attrkey"
	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/devsy-org/devsy/pkg/pool/egress"
)

const (
	// DefaultInterval is the window for coalescing denials and for allow
	// summaries.
	DefaultInterval = time.Minute
	// DefaultMaxKeys caps the distinct keys held in one window.
	DefaultMaxKeys = 256
	maxPort        = 65535
)

var _ egress.Recorder = (*Recorder)(nil)

// Sink writes one audit event. *audit.Writer is a Sink.
type Sink interface {
	Write(audit.Event) error
}

// Config sets one session's recorder.
type Config struct {
	Sink    Sink
	Session string
	Actor   envelope.Actor
	// Interval is the coalescing window. Zero means DefaultInterval.
	Interval time.Duration
	// MaxKeys caps distinct keys per window. Zero means DefaultMaxKeys.
	MaxKeys int
	// ShowAddress says whether a host name or IP literal may appear in an
	// audit event. Nil means never, so every event carries
	// devsy.egress.address_redacted. Set it to the devsy-signals token scan
	// once that exists.
	ShowAddress func(host string) bool
	Now         func() time.Time
	Logger      *slog.Logger
}

// Stats counts what the recorder could not write as events.
type Stats struct {
	// Dropped counts denials past the key cap in a window.
	Dropped int
	// Unrecorded counts decisions the audit allow-list cannot hold: a
	// destination with no valid port or an unknown reason.
	Unrecorded int
	// Failures counts consecutive failed audit writes. A write that succeeds
	// sets it to 0.
	Failures int
}

type dest struct {
	host string
	port int
}

type denyKey struct {
	dest
	reason egress.Reason
}

type tally struct {
	count int
	first time.Time
	last  time.Time
}

// Recorder implements egress.Recorder for one session. It writes the first
// denial of each kind in a window at once, counts allowed connections and
// writes one summary per destination when Flush runs. Callers run Run, or call
// Flush on their own timer, since a window ends only at a flush.
type Recorder struct {
	cfg Config

	flushMu sync.Mutex
	mu      sync.Mutex
	allowed map[dest]tally
	seen    map[denyKey]struct{}
	stats   Stats
}

// New checks the config.
func New(cfg Config) (*Recorder, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Recorder{
		cfg:     cfg.withDefaults(),
		allowed: map[dest]tally{},
		seen:    map[denyKey]struct{}{},
	}, nil
}

func (c Config) validate() error {
	switch {
	case c.Sink == nil:
		return errors.New("egress events need a sink")
	case c.Session == "":
		return errors.New("egress events need a session")
	case !c.Actor.Kind.Known() || c.Actor.ID == "":
		return errors.New("egress events need an actor")
	case c.Interval < 0 || c.MaxKeys < 0:
		return errors.New("egress events limits must not be negative")
	}
	return nil
}

func (c Config) withDefaults() Config {
	c.Interval = cmp.Or(c.Interval, DefaultInterval)
	c.MaxKeys = cmp.Or(c.MaxKeys, DefaultMaxKeys)
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return c
}

// Stats returns a copy of the counters.
func (r *Recorder) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// Denied writes an egress.deny event unless the same destination and reason
// was already written in this window. A failed write is counted, and the next
// matching denial tries again.
func (r *Recorder) Denied(d egress.Denial) {
	key, ok := r.denyKey(d)
	if !ok || !r.claim(key) {
		return
	}
	data := destData(key.dest)
	data[attrkey.EgressReason] = string(key.reason)
	if err := r.write(envelope.AuditEgressDeny, data); err != nil {
		r.release(key)
	}
}

// Allowed counts one tunnel that passed every check.
func (r *Recorder) Allowed(t egress.Target) {
	if !validPort(t.Port) {
		r.unrecorded()
		return
	}
	d := dest{host: r.visible(t), port: t.Port}
	now := r.cfg.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addLocked(d, tally{count: 1, first: now, last: now})
}

// Flush writes one egress.allow.summary per destination counted since the last
// flush and starts a new window. A destination whose write fails, and every
// one after it, stays counted for the next flush.
func (r *Recorder) Flush() error {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	pending := r.take()
	dests := slices.SortedFunc(maps.Keys(pending), compareDest)
	for i, d := range dests {
		if err := r.write(
			envelope.AuditEgressAllowSummary,
			summaryData(d, pending[d]),
		); err != nil {
			r.restore(dests[i:], pending)
			return err
		}
	}
	return nil
}

// Run flushes every Interval until ctx ends, then flushes once more and
// returns that flush's error.
func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return r.Flush()
		case <-ticker.C:
			if err := r.Flush(); err != nil {
				r.cfg.Logger.Warn("egress summary flush failed", "error", err.Error())
			}
		}
	}
}

func (r *Recorder) denyKey(d egress.Denial) (denyKey, bool) {
	if !validPort(d.Port) || !validReason(d.Reason) {
		r.unrecorded()
		return denyKey{}, false
	}
	return denyKey{dest: dest{host: r.visible(d.Target), port: d.Port}, reason: d.Reason}, true
}

// visible returns the host to put in an event, or "" when the address must be
// redacted.
func (r *Recorder) visible(t egress.Target) string {
	if t.AddressRedacted || t.Host == "" || r.cfg.ShowAddress == nil || !r.cfg.ShowAddress(t.Host) {
		return ""
	}
	return t.Host
}

func (r *Recorder) claim(key denyKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen[key]; ok {
		return false
	}
	if len(r.seen) >= r.cfg.MaxKeys {
		r.stats.Dropped++
		return false
	}
	r.seen[key] = struct{}{}
	return true
}

func (r *Recorder) release(key denyKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.seen, key)
}

func (r *Recorder) unrecorded() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.Unrecorded++
}

// addLocked merges t into the allowed counts. A new named destination past the
// cap folds into the redacted bucket of its port, so the count stays right and
// the map stays bounded by the number of allowed ports.
func (r *Recorder) addLocked(d dest, t tally) {
	if _, ok := r.allowed[d]; !ok && d.host != "" && len(r.allowed) >= r.cfg.MaxKeys {
		d.host = ""
	}
	cur, ok := r.allowed[d]
	if !ok {
		r.allowed[d] = t
		return
	}
	cur.count += t.count
	if t.first.Before(cur.first) {
		cur.first = t.first
	}
	if t.last.After(cur.last) {
		cur.last = t.last
	}
	r.allowed[d] = cur
}

func (r *Recorder) take() map[dest]tally {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.allowed
	r.allowed = map[dest]tally{}
	r.seen = map[denyKey]struct{}{}
	return pending
}

func (r *Recorder) restore(dests []dest, pending map[dest]tally) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range dests {
		r.addLocked(d, pending[d])
	}
}

func (r *Recorder) write(typ string, data map[string]any) error {
	err := r.cfg.Sink.Write(audit.Event{
		Type:    typ,
		Session: r.cfg.Session,
		Actor:   r.cfg.Actor,
		Data:    data,
	})
	r.mu.Lock()
	if err != nil {
		r.stats.Failures++
	} else {
		r.stats.Failures = 0
	}
	r.mu.Unlock()
	if err != nil {
		r.cfg.Logger.Warn("egress audit write failed", "type", typ, "error", err.Error())
	}
	return err
}

func destData(d dest) map[string]any {
	data := map[string]any{attrkey.ServerPort: d.port}
	if d.host == "" {
		data[attrkey.EgressAddressRedacted] = true
	} else {
		data[attrkey.ServerAddress] = d.host
	}
	return data
}

func summaryData(d dest, t tally) map[string]any {
	data := destData(d)
	data[attrkey.EgressCount] = t.count
	data[attrkey.EgressFirstSeen] = envelope.FormatTime(t.first)
	data[attrkey.EgressLastSeen] = envelope.FormatTime(t.last)
	return data
}

func compareDest(a, b dest) int {
	return cmp.Or(cmp.Compare(a.host, b.host), cmp.Compare(a.port, b.port))
}

func validPort(port int) bool { return port >= 1 && port <= maxPort }

func validReason(r egress.Reason) bool {
	switch r {
	case egress.NotAllowed, egress.ReservedRange, egress.PrivateRange, egress.PoolNetwork,
		egress.IPLiteral, egress.ResolveFailed, egress.BadRequest, egress.SNIMismatch,
		egress.SNIMissing:
		return true
	}
	return false
}
