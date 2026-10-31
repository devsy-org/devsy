package workspacelogs

import (
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/devsy-org/devsy/pkg/secrets"
)

// Backend is the persistence a Recorder writes to. *Store implements it.
type Backend interface {
	RegisterProducer(runID string, ref ProducerRef) error
	Append(records []Record) (AppendResult, error)
	AddCondition(runID string, c Condition) error
}

// RecorderOptions configures a Recorder.
type RecorderOptions struct {
	Backend  Backend
	RunID    string
	Location Location
	// ProducerID uses a producer ID its parent reserved earlier. Empty
	// generates one.
	ProducerID string
	// Resumed marks a worker that continues a run whose launching process
	// already recorded the plan. Plan-time transitions are not repeated.
	Resumed bool
	// ParentProducerID links this producer to the one that launched it.
	ParentProducerID string
	// Redactor masks known secrets before anything is persisted. Nil keeps text
	// as written.
	Redactor *secrets.Redactor
	// Now overrides the clock in tests.
	Now func() time.Time
}

// Invocation describes one hook or build step transition.
type Invocation struct {
	ID       string
	Phase    string
	Hook     string
	Index    *int
	Key      string
	State    InvocationState
	Reason   string
	ExitCode *int
	Signal   string
	Duration time.Duration
}

// Recorder writes one producer's records. It assigns the producer sequence and
// never fails the caller: the first storage error disables the recorder, is
// kept for Err, and later calls become no-ops so the recorded command's own
// error and exit semantics are untouched.
type Recorder struct {
	opts       RecorderOptions
	producerID string

	mu       sync.Mutex
	seq      uint64
	err      error
	disabled bool
}

// NewRecorder registers a new producer in the run and records that it is
// running. A failure to register returns a recorder that is already disabled
// together with the error, so callers may ignore the recorder's state.
func NewRecorder(opts RecorderOptions) (*Recorder, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	id := opts.ProducerID
	if !ValidID(id) {
		id = NewID()
	}
	r := &Recorder{opts: opts, producerID: id}
	err := opts.Backend.RegisterProducer(opts.RunID, ProducerRef{
		ProducerID:       r.producerID,
		ParentProducerID: opts.ParentProducerID,
		State:            ProducerRunning,
		Location:         opts.Location,
	})
	if err != nil {
		r.fail(err)

		return r, err
	}
	r.emit(&Record{
		Kind: KindProducer, ProducerState: ProducerRunning,
		ParentProducerID: opts.ParentProducerID, Location: opts.Location,
	})

	return r, nil
}

// ProducerID returns the identity of this producer.
func (r *Recorder) ProducerID() string { return r.producerID }

// Err returns the first storage error, if any.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.err
}

// Invocation records a transition of one invocation, including silent ones.
func (r *Recorder) Invocation(inv Invocation) {
	if r.opts.Resumed && repeatsPlan(inv) {
		return
	}
	rec := &Record{
		Kind:         KindInvocation,
		InvocationID: inv.ID,
		Phase:        inv.Phase,
		Hook:         inv.Hook,
		HookIndex:    inv.Index,
		HookKey:      truncateDetail(inv.Key),
		Location:     r.opts.Location,
		State:        inv.State,
		Reason:       truncateDetail(inv.Reason),
		ExitCode:     inv.ExitCode,
		Signal:       inv.Signal,
	}
	if inv.Duration > 0 {
		ms := inv.Duration.Milliseconds()
		rec.DurationMillis = &ms
	}
	r.emit(rec)
}

// Capture records a condition that makes the run's capture partial.
func (r *Recorder) Capture(c Condition, detail string, droppedBytes int64) {
	r.emit(&Record{
		Kind:         KindCapture,
		Condition:    c,
		Detail:       truncateDetail(detail),
		DroppedBytes: droppedBytes,
	})
}

// ReserveChild records that a child producer will be launched and returns its
// ID. Pass the ID to the child as RecorderOptions.ProducerID. Reserve every
// child before sealing discovery.
func (r *Recorder) ReserveChild(location Location) string {
	id := NewID()
	r.emit(&Record{
		Kind:            KindProducer,
		ChildProducerID: id,
		ProducerState:   ProducerReserved,
		Location:        location,
	})

	return id
}

// MarkChild records a factual outcome for a reserved child, for example
// ProducerUnavailable when its launch was not observed. Reason is shown to
// readers.
func (r *Recorder) MarkChild(id string, state ProducerState, reason string) {
	r.emit(&Record{
		Kind:            KindProducer,
		ChildProducerID: id,
		ProducerState:   state,
		Detail:          truncateDetail(reason),
	})
}

// SealDiscovery records that this producer has reserved every child it will
// launch.
func (r *Recorder) SealDiscovery() {
	r.emit(&Record{Kind: KindProducer, DiscoverySealed: true})
}

// Close records the producer's final state. Close any DiagnosticWriter first.
func (r *Recorder) Close(state ProducerState, sealDiscovery bool) {
	r.emit(&Record{Kind: KindProducer, ProducerState: state, DiscoverySealed: sealDiscovery})
}

// Writer returns a writer for one invocation's stream. Each writer redacts on
// its own, so a secret split across writes is masked and streams do not mix.
func (r *Recorder) Writer(invocationID string, stream Stream) *DiagnosticWriter {
	return &DiagnosticWriter{
		rec: r, invocationID: invocationID, stream: stream,
		redactor: secrets.NewStreamingRedactor(r.opts.Redactor),
	}
}

func (r *Recorder) emit(rec *Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disabled {
		return
	}
	r.seq++
	rec.SchemaVersion = SchemaVersion
	rec.RunID = r.opts.RunID
	rec.ProducerID = r.producerID
	rec.Sequence = r.seq
	rec.Time = r.opts.Now().UTC()
	if _, err := r.opts.Backend.Append([]Record{*rec}); err != nil {
		if rec.Kind == KindOutput && errors.Is(err, ErrCapacity) {
			r.seq--
			_ = r.opts.Backend.AddCondition(r.opts.RunID, ConditionCapacity)

			return
		}
		r.failLocked(err)
	}
}

func (r *Recorder) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failLocked(err)
}

func (r *Recorder) failLocked(err error) {
	if r.err == nil {
		r.err = err
		// Best effort: the store may be the thing that failed.
		_ = r.opts.Backend.AddCondition(r.opts.RunID, ConditionWriteError)
	}
	r.disabled = true
}

// DiagnosticWriter adapts a Recorder to io.WriteCloser for one invocation
// stream. Write always reports success so a failing store cannot change the
// behavior of the process producing the output.
type DiagnosticWriter struct {
	rec          *Recorder
	invocationID string
	stream       Stream
	redactor     *secrets.StreamingRedactor

	mu      sync.Mutex
	partial []byte
	closed  bool
}

var _ io.WriteCloser = (*DiagnosticWriter)(nil)

// Write redacts p and records it.
func (w *DiagnosticWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return len(p), nil
	}
	data := append(w.partial, p...) //nolint:gocritic // partial is replaced below
	cut := completeUTF8(data)
	w.partial = append([]byte(nil), data[cut:]...)
	w.record(w.redactor.RedactChunk(strings.ToValidUTF8(string(data[:cut]), "�")))

	return len(p), nil
}

// Close flushes buffered text, including a final fragment without a newline.
func (w *DiagnosticWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	tail := w.redactor.RedactChunk(strings.ToValidUTF8(string(w.partial), "�"))
	w.partial = nil
	w.record(tail + w.redactor.Flush())

	return nil
}

func (w *DiagnosticWriter) record(text string) {
	for _, chunk := range SplitText(text, MaxChunkBytes) {
		w.rec.emit(&Record{
			Kind: KindOutput, InvocationID: w.invocationID, Stream: w.stream,
			Text: chunk, Bytes: len(chunk),
		})
	}
}

// completeUTF8 returns the length of the longest prefix of data that does not
// end inside a multi-byte sequence.
func completeUTF8(data []byte) int {
	for back := 1; back <= utf8.UTFMax-1 && back <= len(data); back++ {
		i := len(data) - back
		if utf8.RuneStart(data[i]) {
			if utf8.FullRune(data[i:]) {
				return len(data)
			}

			return i
		}
	}

	return len(data)
}

func truncateDetail(s string) string {
	const maxBytes = MaxDetailBytes
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut]
}

// planSkipReasons are skip reasons decided from configuration alone, so the
// process that planned the phase has already recorded them. Reasons that
// depend on state seen by a later process (a once-marker) are not listed.
var planSkipReasons = map[string]bool{
	"no_command_configured": true,
	"explicit_skip":         true,
	"prebuild_excluded":     true,
	"recovery_excluded":     true,
}

func repeatsPlan(inv Invocation) bool {
	switch inv.State {
	case InvocationPending, InvocationDeferred:
		return true
	case InvocationSkipped:
		return planSkipReasons[inv.Reason]
	default:
		return false
	}
}
