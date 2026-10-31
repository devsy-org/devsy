package workspacelogs

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ErrSourceGap means the next records a sender needs are no longer retained
// at its own store, so mirroring cannot continue past them.
var ErrSourceGap = errors.New("source records are not retained")

// StoreSource reads a producer's committed records back from a Store in
// bounded batches for mirroring.
type StoreSource struct {
	store *Store
}

// NewStoreSource returns a source over store.
func NewStoreSource(store *Store) *StoreSource { return &StoreSource{store: store} }

// ReadAfter returns the contiguous records of one producer after sequence
// after, at most maxBytes when JSON encoded. It returns ErrSourceGap when the
// first record needed is missing or unreadable.
func (s *StoreSource) ReadAfter(
	runID, producerID string, after uint64, maxBytes int,
) ([]Record, error) {
	b := &batchCollector{producerID: producerID, after: after, maxBytes: maxBytes}
	cursor := ""
	for {
		resp, err := s.store.Read(ReadRequest{RunID: runID, After: cursor, Limit: MaxReadLimit})
		if err != nil {
			return nil, err
		}
		if b.collect(resp.Records) {
			return gapOrBatch(b.out, b.broken)
		}
		if !resp.HasMore {
			return b.out, nil
		}
		cursor = resp.NextCursor
	}
}

// batchCollector gathers one producer's contiguous records from read pages.
type batchCollector struct {
	producerID string
	after      uint64
	maxBytes   int
	used       int
	out        []Record
	broken     bool
}

// collect adds the producer's records from one page. It returns true once the
// batch is full or the sequence breaks.
func (b *batchCollector) collect(page []Record) bool {
	for i := range page {
		rec := &page[i]
		if rec.ProducerID == b.producerID && rec.Sequence > b.after && b.add(rec) {
			return true
		}
	}

	return false
}

func (b *batchCollector) add(rec *Record) bool {
	// Records synthesized by the reader for holes carry no time.
	if rec.Sequence != b.after+uint64(len(b.out))+1 || rec.Time.IsZero() {
		b.broken = true

		return true
	}
	data, err := json.Marshal(rec)
	if err != nil || (b.used+len(data) > b.maxBytes && len(b.out) > 0) {
		return true
	}
	b.used += len(data)
	b.out = append(b.out, *rec)

	return false
}

func gapOrBatch(out []Record, broken bool) ([]Record, error) {
	if broken && len(out) == 0 {
		return nil, ErrSourceGap
	}

	return out, nil
}

// MirrorResult is the typed outcome of one batch.
type MirrorResult string

// Batch outcomes.
const (
	// MirrorAccepted means records up to Accepted are persisted. Accepted can be
	// lower than the batch's last sequence when the receiver was behind.
	MirrorAccepted MirrorResult = "accepted"
	// MirrorRetentionGap means the receiver will never hold the gap range.
	MirrorRetentionGap MirrorResult = "retention_gap"
	// MirrorRejected means the batch is invalid and must not be retried.
	MirrorRejected MirrorResult = "rejected_invalid"
)

// BatchRequest is one contiguous batch of a producer's encoded records.
type BatchRequest struct {
	SchemaVersion int
	RunID         string
	ProducerID    string
	FirstSequence uint64
	LastSequence  uint64
	// Records holds JSON encoded Record values.
	Records [][]byte
}

// BatchResult is the receiver's answer.
type BatchResult struct {
	Result MirrorResult
	// Accepted is the highest contiguous sequence persisted for the producer.
	Accepted uint64
	GapFirst uint64
	GapLast  uint64
	Detail   string
}

// batchOverhead is the allowance for the request envelope around the records.
const batchOverhead = 1024

// SinkOptions configures a Sink.
type SinkOptions struct {
	Store *Store
	// Spec identifies the run this host expects. RunID is required; the run is
	// created or joined at construction.
	Spec RunSpec
	// Acks persists the inbound position and retention gaps. Optional.
	Acks *AckStore
	// OnAccepted is called after records are persisted, so a forwarding host can
	// queue them for the next hop. It must not block.
	OnAccepted func(producerID string, accepted uint64)
}

// Sink validates incoming batches against the expected run and persists them
// idempotently. It acknowledges only what reached its store or was recorded as
// a gap.
type Sink struct {
	opts SinkOptions

	mu    sync.Mutex
	known map[string]bool
}

// NewSink creates or joins the expected run and returns its sink.
func NewSink(opts SinkOptions) (*Sink, error) {
	if opts.Store == nil || opts.Spec.RunID == "" {
		return nil, fmt.Errorf("%w: sink needs a store and a run ID", ErrInvalidRecord)
	}
	if _, err := opts.Store.CreateRun(opts.Spec); err != nil {
		return nil, err
	}

	return &Sink{opts: opts, known: map[string]bool{}}, nil
}

// RunID returns the run this sink accepts.
func (s *Sink) RunID() string { return s.opts.Spec.RunID }

// Append validates and persists one batch. A non-nil error is a transient
// storage failure the sender may retry.
func (s *Sink) Append(req BatchRequest) (BatchResult, error) {
	records, reject := s.decode(req)
	if reject != "" {
		return BatchResult{Result: MirrorRejected, Detail: reject}, nil
	}
	if gap, ok := s.excluded(req); ok {
		return gap, nil
	}
	if res, done, err := s.register(req.ProducerID, &records[0]); done {
		return res, err
	}
	res, err := s.opts.Store.Append(records)

	return s.outcome(req, res, err)
}

// decode checks identity, sizes and record types. The second result is a
// rejection reason, empty when the batch is valid.
func (s *Sink) decode(req BatchRequest) ([]Record, string) {
	if reason := s.checkHeader(req); reason != "" {
		return nil, reason
	}
	total := batchOverhead
	records := make([]Record, len(req.Records))
	for i, raw := range req.Records {
		if total += len(raw); total > MaxPageBytes {
			return nil, "batch exceeds page size"
		}
		if reason := decodeRecord(raw, req, i, &records[i]); reason != "" {
			return nil, reason
		}
	}

	return records, ""
}

func (s *Sink) checkHeader(req BatchRequest) string {
	n := len(req.Records)
	switch {
	case req.SchemaVersion != SchemaVersion:
		return "unsupported schema version"
	case req.RunID != s.opts.Spec.RunID:
		return "unexpected run"
	case !ValidID(req.ProducerID):
		return "invalid producer"
	case n == 0 || req.FirstSequence == 0 || req.LastSequence < req.FirstSequence ||
		req.LastSequence-req.FirstSequence+1 != uint64(n): //nolint:gosec // n is a slice length
		return "inconsistent sequence range"
	}

	return ""
}

func decodeRecord(raw []byte, req BatchRequest, i int, rec *Record) string {
	if err := json.Unmarshal(raw, rec); err != nil {
		return "undecodable record"
	}
	if rec.RunID != req.RunID || rec.ProducerID != req.ProducerID ||
		rec.Sequence != req.FirstSequence+uint64(i) { //nolint:gosec // i is a slice index
		return "record identity does not match the batch"
	}
	if err := rec.Validate(); err != nil {
		return err.Error()
	}
	if rec.Kind != KindOutput && len(raw) > MaxMetadataBytes {
		return "metadata record is too large"
	}

	return ""
}

// excluded answers batches for a producer whose bytes the store already gave
// up on, so the sender stops retrying them.
func (s *Sink) excluded(req BatchRequest) (BatchResult, bool) {
	if s.opts.Acks == nil {
		return BatchResult{}, false
	}
	st, err := s.opts.Acks.Load(
		AckKey{RunID: req.RunID, ProducerID: req.ProducerID, Hop: HopInbound},
	)
	if err != nil || !st.Gapped {
		return BatchResult{}, false
	}
	first := max(req.FirstSequence, st.Acked+1)
	if first > req.LastSequence {
		return BatchResult{
			Result: MirrorRetentionGap, Accepted: st.Acked,
			GapFirst: st.GapFirst, GapLast: st.GapLast, Detail: "producer excluded by retention",
		}, true
	}

	return BatchResult{
		Result: MirrorRetentionGap, Accepted: req.LastSequence,
		GapFirst: first, GapLast: req.LastSequence, Detail: "producer excluded by retention",
	}, true
}

// register adds the producer to the run before its first batch. done reports
// that the batch is answered without appending.
func (s *Sink) register(producerID string, first *Record) (BatchResult, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.known[producerID] {
		return BatchResult{}, false, nil
	}
	ref := ProducerRef{ProducerID: producerID, State: ProducerRunning, Location: first.Location}
	if first.Kind == KindProducer {
		ref.ParentProducerID = first.ParentProducerID
	}
	err := s.opts.Store.RegisterProducer(s.opts.Spec.RunID, ref)
	switch {
	case err == nil:
		s.known[producerID] = true

		return BatchResult{}, false, nil
	case errors.Is(err, ErrProducerCap):
		return BatchResult{
			Result: MirrorRetentionGap, GapFirst: first.Sequence,
			GapLast: first.Sequence, Detail: "producer limit reached",
		}, true, nil
	case errors.Is(err, ErrDiscoverySealed), errors.Is(err, ErrInvalidID):
		return BatchResult{Result: MirrorRejected, Detail: err.Error()}, true, nil
	default:
		return BatchResult{}, true, err
	}
}

func (s *Sink) outcome(req BatchRequest, res AppendResult, err error) (BatchResult, error) {
	switch {
	case err == nil:
		s.persisted(req, res.Accepted)

		return BatchResult{Result: MirrorAccepted, Accepted: res.Accepted}, nil
	case errors.Is(err, ErrSequenceGap):
		// Behind the batch: report what is held so the sender resends from there.
		return BatchResult{Result: MirrorAccepted, Accepted: res.Accepted}, nil
	case errors.Is(err, ErrCapacity):
		return s.gap(req, res.Accepted), nil
	case errors.Is(err, ErrInvalidRecord):
		return BatchResult{Result: MirrorRejected, Accepted: res.Accepted, Detail: err.Error()}, nil
	default:
		s.persisted(req, res.Accepted)

		return BatchResult{}, err
	}
}

func (s *Sink) persisted(req BatchRequest, accepted uint64) {
	if accepted == 0 {
		return
	}
	if s.opts.Acks != nil {
		_ = s.opts.Acks.Advance(
			AckKey{RunID: req.RunID, ProducerID: req.ProducerID, Hop: HopInbound},
			accepted,
		)
	}
	if s.opts.OnAccepted != nil {
		s.opts.OnAccepted(req.ProducerID, accepted)
	}
}

// gap records that the store cannot take the rest of the batch. Capture is
// marked partial and the position moves past the range.
func (s *Sink) gap(req BatchRequest, accepted uint64) BatchResult {
	s.persisted(req, accepted)
	first := max(req.FirstSequence, accepted+1)
	_ = s.opts.Store.AddCondition(req.RunID, ConditionRetentionGap)
	if s.opts.Acks != nil {
		_ = s.opts.Acks.RecordGap(
			AckKey{RunID: req.RunID, ProducerID: req.ProducerID, Hop: HopInbound},
			first,
			req.LastSequence,
		)
	}

	return BatchResult{
		Result: MirrorRetentionGap, Accepted: req.LastSequence,
		GapFirst: first, GapLast: req.LastSequence, Detail: "retention excluded the batch",
	}
}
