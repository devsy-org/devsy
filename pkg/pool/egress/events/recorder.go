// Package events turns the egress proxy's decisions into audit events.
package events

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/netip"
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
	// DefaultMaxKeys caps the distinct denial keys held per reason in one
	// window, and the distinct named destinations counted as allowed.
	DefaultMaxKeys = 256
	maxPort        = 65535
	// maxAttempts is how many times one event is written before it is given
	// up. The audit writer has no typed validation error, so a poison event
	// is told from a down store only by repeated failure.
	maxAttempts = 3
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
	// MaxKeys caps distinct denial keys per reason and distinct named
	// allowed destinations per window. Redacted allowed destinations may
	// double that. Zero means DefaultMaxKeys.
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
	// Dropped counts decisions past a window's key budget: denials past the
	// budget of their reason, and allowed connections past the destination
	// limit. A flush logs any increase.
	Dropped int
	// Lost counts events given up after maxAttempts failed writes.
	Lost int
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
	// rule is set only on allowed destinations. Two rules never share a
	// summary, so rule_index stays true for the whole count.
	rule egress.Rule
}

type denyKey struct {
	dest
	reason egress.Reason
}

type tally struct {
	count int
	first time.Time
	last  time.Time
	tries int
}

type pendingDeny struct {
	data  map[string]any
	tries int
}

// Recorder implements egress.Recorder for one session. It writes the first
// denial of each kind in a window at once, counts allowed connections and
// writes one summary per destination when Flush runs. Callers run Run, or call
// Flush on their own timer, since a window ends only at a flush.
//
// A repeat of a denial in the same window is not written, and egress.deny has
// no count key, so the repeat count is not recorded. The peer address in the
// event is the first one seen for that key in the window. Each reason has its
// own key budget, so denials of one reason cannot hide those of another.
//
// rule_index on a summary is the first host or cidr rule that matched. For a
// private answer the cidr rule that also had to allow it is not reported.
type Recorder struct {
	cfg Config

	flushMu  sync.Mutex
	mu       sync.Mutex
	allowed  map[dest]tally
	seen     map[denyKey]struct{}
	budget   map[egress.Reason]int
	retry    map[denyKey]pendingDeny
	stats    Stats
	reported Stats
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
		budget:  map[egress.Reason]int{},
		retry:   map[denyKey]pendingDeny{},
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
// was already written in this window. A failed write is counted and the event
// is tried again at the next flush, not on the next matching denial.
func (r *Recorder) Denied(d egress.Denial) {
	key, ok := r.denyKey(d)
	if !ok || !r.claim(key) {
		return
	}
	data := destData(key.dest)
	data[attrkey.EgressReason] = string(key.reason)
	if peer, ok := peerText(d.Peer); ok {
		data[attrkey.NetworkPeerAddress] = peer
	}
	if err := r.write(envelope.AuditEgressDeny, data); err != nil {
		r.queueRetry(key, pendingDeny{data: data, tries: 1})
	}
}

// Allowed counts one tunnel that passed every check.
func (r *Recorder) Allowed(a egress.Allow) {
	if !validPort(a.Port) {
		r.unrecorded()
		return
	}
	d := dest{host: r.visible(a.Target), port: a.Port, rule: a.Rule}
	now := r.cfg.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addLocked(d, tally{count: 1, first: now, last: now})
}

// Flush writes the denials whose first write failed, then one
// egress.allow.summary per destination counted since the last flush, and
// starts a new window. After a failed write, that event and every one after
// it stay queued for the next flush. An event that fails maxAttempts times is
// given up and counted in Stats.Lost, so one event the writer refuses cannot
// block the rest. Flush logs any rise in Dropped, Unrecorded and Lost.
func (r *Recorder) Flush() error {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	retries, pending := r.take()
	r.reportLoss()
	dests := slices.SortedFunc(maps.Keys(pending), compareDest)
	if err := r.flushDenials(retries); err != nil {
		r.restore(dests, pending, false)
		return err
	}
	for i, d := range dests {
		if err := r.write(
			envelope.AuditEgressAllowSummary,
			summaryData(d, pending[d]),
		); err != nil {
			r.restore(dests[i:], pending, true)
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

func (r *Recorder) flushDenials(retries map[denyKey]pendingDeny) error {
	keys := slices.SortedFunc(maps.Keys(retries), compareDenyKey)
	for i, key := range keys {
		if err := r.write(envelope.AuditEgressDeny, retries[key].data); err != nil {
			r.requeue(keys[i:], retries)
			return err
		}
	}
	return nil
}

func (r *Recorder) reportLoss() {
	r.mu.Lock()
	now, before := r.stats, r.reported
	r.reported = now
	r.mu.Unlock()
	if now.Dropped == before.Dropped && now.Unrecorded == before.Unrecorded &&
		now.Lost == before.Lost {
		return
	}
	r.cfg.Logger.Warn("egress audit events not recorded",
		"dropped", now.Dropped-before.Dropped,
		"unrecorded", now.Unrecorded-before.Unrecorded,
		"lost", now.Lost-before.Lost)
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
	if r.budget[key.reason] >= r.cfg.MaxKeys {
		r.stats.Dropped++
		return false
	}
	r.claimLocked(key)
	return true
}

func (r *Recorder) claimLocked(key denyKey) {
	r.seen[key] = struct{}{}
	r.budget[key.reason]++
}

func (r *Recorder) queueRetry(key denyKey, p pendingDeny) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retry[key] = p
}

// requeue holds the denials from a failed flush for the next one. The first
// is the one that failed, so it has used another attempt.
func (r *Recorder) requeue(keys []denyKey, retries map[denyKey]pendingDeny) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, key := range keys {
		p := retries[key]
		if i == 0 {
			p.tries++
		}
		if p.tries >= maxAttempts {
			r.loseLocked(envelope.AuditEgressDeny)
			continue
		}
		r.claimLocked(key)
		r.retry[key] = p
	}
}

func (r *Recorder) loseLocked(typ string) {
	r.stats.Lost++
	r.cfg.Logger.Warn("egress audit event given up", "type", typ)
}

func (r *Recorder) unrecorded() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.Unrecorded++
}

// addLocked merges t into the allowed counts. A new named destination past
// MaxKeys folds into the redacted bucket of its port, so the count stays
// right. A new bucket past twice MaxKeys is not kept and is counted in
// Stats.Dropped, so the map stays bounded whatever ports the policy allows.
func (r *Recorder) addLocked(d dest, t tally) {
	if _, ok := r.allowed[d]; !ok && d.host != "" && len(r.allowed) >= r.cfg.MaxKeys {
		d.host = ""
	}
	cur, ok := r.allowed[d]
	if !ok {
		if len(r.allowed) >= 2*r.cfg.MaxKeys {
			r.stats.Dropped += t.count
			return
		}
		r.allowed[d] = t
		return
	}
	cur.count += t.count
	cur.tries = max(cur.tries, t.tries)
	if t.first.Before(cur.first) {
		cur.first = t.first
	}
	if t.last.After(cur.last) {
		cur.last = t.last
	}
	r.allowed[d] = cur
}

func (r *Recorder) take() (map[denyKey]pendingDeny, map[dest]tally) {
	r.mu.Lock()
	defer r.mu.Unlock()
	retries, pending := r.retry, r.allowed
	r.retry = map[denyKey]pendingDeny{}
	r.allowed = map[dest]tally{}
	r.seen = map[denyKey]struct{}{}
	r.budget = map[egress.Reason]int{}
	return retries, pending
}

// restore puts counts back for the next flush. When failed is set the first
// destination is the one whose write failed, so it has used another attempt.
func (r *Recorder) restore(dests []dest, pending map[dest]tally, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, d := range dests {
		t := pending[d]
		if failed && i == 0 {
			t.tries++
		}
		if t.tries >= maxAttempts {
			r.loseLocked(envelope.AuditEgressAllowSummary)
			continue
		}
		r.addLocked(d, t)
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
	if index, ok := d.rule.Index(); ok {
		data[attrkey.EgressRuleIndex] = index
	}
	data[attrkey.EgressCount] = t.count
	data[attrkey.EgressFirstSeen] = envelope.FormatTime(t.first)
	data[attrkey.EgressLastSeen] = envelope.FormatTime(t.last)
	return data
}

func compareDest(a, b dest) int {
	return cmp.Or(
		cmp.Compare(a.host, b.host),
		cmp.Compare(a.port, b.port),
		cmp.Compare(ruleOrder(a.rule), ruleOrder(b.rule)),
	)
}

func ruleOrder(r egress.Rule) int {
	if index, ok := r.Index(); ok {
		return index
	}
	return -1
}

// peerText returns the address text for network.peer.address. A zone is
// client-controlled text and an IPv4-mapped form is not the address dialed,
// so neither is passed on.
func peerText(addr netip.Addr) (string, bool) {
	if !addr.IsValid() || addr.Zone() != "" {
		return "", false
	}
	return addr.Unmap().String(), true
}

func compareDenyKey(a, b denyKey) int {
	return cmp.Or(compareDest(a.dest, b.dest), cmp.Compare(a.reason, b.reason))
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
