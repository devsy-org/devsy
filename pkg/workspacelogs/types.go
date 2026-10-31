// Package workspacelogs retains bounded, redacted build and lifecycle output
// for workspace runs. It owns the on-disk record format, per-run manifests,
// append and read paths, retention, and typed cursors. It does not execute
// hooks or own a background service.
package workspacelogs

import (
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// SchemaVersion is the version written to every manifest, record and response.
const SchemaVersion = 1

// Kind identifies the shape of a Record.
type Kind string

// Record kinds.
const (
	KindProducer   Kind = "producer"
	KindInvocation Kind = "invocation"
	KindOutput     Kind = "output"
	KindCapture    Kind = "capture"
)

// Stream names the origin of output text.
type Stream string

// Output streams. StreamCombined is only for upstream producers that already
// merge stdout and stderr.
const (
	StreamStdout   Stream = "stdout"
	StreamStderr   Stream = "stderr"
	StreamCombined Stream = "combined"
)

// InvocationState is the lifecycle state of one hook or build invocation.
type InvocationState string

// Invocation states.
const (
	InvocationPending   InvocationState = "pending"
	InvocationDeferred  InvocationState = "deferred"
	InvocationRunning   InvocationState = "running"
	InvocationSucceeded InvocationState = "succeeded"
	InvocationFailed    InvocationState = "failed"
	InvocationSkipped   InvocationState = "skipped"
	InvocationCanceled  InvocationState = "canceled"
	InvocationUnknown   InvocationState = "completion_unknown"
)

// ProducerState is the lifecycle state of one producer (a process or worker).
type ProducerState string

// Producer states.
const (
	ProducerReserved    ProducerState = "reserved"
	ProducerRunning     ProducerState = "running"
	ProducerTerminal    ProducerState = "terminal"
	ProducerUnavailable ProducerState = "unavailable"
)

// CommandState is the outcome of the command that started a run. It is
// separate from hook and capture completion.
type CommandState string

// Command states.
const (
	CommandPending   CommandState = "pending"
	CommandRunning   CommandState = "running"
	CommandSucceeded CommandState = "succeeded"
	CommandFailed    CommandState = "failed"
	CommandCanceled  CommandState = "canceled"
	CommandUnknown   CommandState = "unknown"
)

// CaptureState describes how much of a run's output is retained.
type CaptureState string

// Capture states.
const (
	CapturePending     CaptureState = "pending"
	CaptureRecording   CaptureState = "recording"
	CaptureComplete    CaptureState = "complete"
	CapturePartial     CaptureState = "partial"
	CaptureUnavailable CaptureState = "unavailable"
)

// Condition explains why capture is not complete. It never replaces the
// command outcome.
type Condition string

// Capture conditions.
const (
	ConditionTruncated         Condition = "truncated"
	ConditionRetentionGap      Condition = "retention_gap"
	ConditionWriteError        Condition = "write_error"
	ConditionUnsupportedSource Condition = "unsupported_source"
	ConditionSourceUnavailable Condition = "source_unavailable"
	ConditionInterrupted       Condition = "interrupted"
	ConditionCapacity          Condition = "capacity"
	ConditionProducerCap       Condition = "producer_cap"
	ConditionMalformedRecord   Condition = "malformed_record"
)

// Location is where an invocation executed.
type Location string

// Execution locations.
const (
	LocationHost         Location = "host"
	LocationProviderHost Location = "provider_host"
	LocationContainer    Location = "container"
)

// Size bounds. The values are defaults for the first delivery.
const (
	// MaxChunkBytes is the largest output text in one record.
	MaxChunkBytes = 8 * 1024
	// MaxPageBytes is the largest encoded read or transport page.
	MaxPageBytes = 256 * 1024
	// MaxMetadataBytes is the largest encoded non-output record.
	MaxMetadataBytes = 64 * 1024
	// MaxProducers is the most producers a run records.
	MaxProducers = 64
	// MaxCursorBytes is the largest encoded cursor.
	MaxCursorBytes = 8 * 1024
	// MaxDetailBytes truncates optional free-text fields.
	MaxDetailBytes = 1024
)

// Errors returned by the package.
var (
	ErrInvalidID        = errors.New("invalid identifier")
	ErrInvalidRecord    = errors.New("invalid record")
	ErrNotFound         = errors.New("not found")
	ErrIdentityConflict = errors.New("workspace identity already bound")
	ErrSequenceGap      = errors.New("sequence gap")
	ErrInvalidCursor    = errors.New("invalid cursor")
	ErrInvalidSelector  = errors.New("invalid selector")
	ErrUnsafePath       = errors.New("unsafe storage path")
	ErrCapacity         = errors.New("storage capacity exhausted")
	ErrProducerCap      = errors.New("producer limit reached")
	ErrDiscoverySealed  = errors.New("producer discovery is sealed")
)

// Record is one immutable entry in a producer's stream. Fields outside the
// record's Kind are omitted.
type Record struct {
	SchemaVersion int       `json:"schemaVersion"`
	Kind          Kind      `json:"kind"`
	RunID         string    `json:"runId"`
	ProducerID    string    `json:"producerId"`
	Sequence      uint64    `json:"sequence"`
	Time          time.Time `json:"time"`

	// Producer and invocation transitions.
	ParentProducerID string `json:"parentProducerId,omitempty"`
	// ChildProducerID, on a producer record, reserves (or finalizes) a child
	// producer the writer is about to launch. Mirrored stores learn about the
	// child from this record, before any of its own records arrive.
	ChildProducerID string          `json:"childProducerId,omitempty"`
	ProducerState   ProducerState   `json:"producerState,omitempty"`
	DiscoverySealed bool            `json:"discoverySealed,omitempty"`
	InvocationID    string          `json:"invocationId,omitempty"`
	Phase           string          `json:"phase,omitempty"`
	Hook            string          `json:"hook,omitempty"`
	HookIndex       *int            `json:"hookIndex,omitempty"`
	HookKey         string          `json:"hookKey,omitempty"`
	Location        Location        `json:"location,omitempty"`
	State           InvocationState `json:"state,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	DurationMillis  *int64          `json:"durationMillis,omitempty"`
	ExitCode        *int            `json:"exitCode,omitempty"`
	Signal          string          `json:"signal,omitempty"`

	// Output.
	Stream Stream `json:"stream,omitempty"`
	Text   string `json:"text,omitempty"`
	Bytes  int    `json:"bytes,omitempty"`
	// Elided marks output whose text was dropped for capacity. Sequence and
	// Bytes are kept so producers and readers stay contiguous.
	Elided bool `json:"elided,omitempty"`

	// Capture conditions.
	Condition    Condition `json:"condition,omitempty"`
	Detail       string    `json:"detail,omitempty"`
	DroppedBytes int64     `json:"droppedBytes,omitempty"`
}

var uuidPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// NewID returns a new random lower-case UUID for runs, producers and
// invocations.
func NewID() string { return uuid.NewString() }

// ValidID reports whether id is a canonical lower-case UUID. Every identifier
// that reaches the filesystem passes this check first.
func ValidID(id string) bool { return uuidPattern.MatchString(id) }

func requireID(name, id string) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: %s", ErrInvalidID, name)
	}

	return nil
}

var validHooks = map[string]struct{}{
	"initializeCommand":    {},
	"onCreateCommand":      {},
	"updateContentCommand": {},
	"postCreateCommand":    {},
	"postStartCommand":     {},
	"postAttachCommand":    {},
}

// ValidHook reports whether name is an exact lifecycle property name.
func ValidHook(name string) bool {
	_, ok := validHooks[name]

	return ok
}

// Validate checks structural bounds that do not depend on stored state.
func (r *Record) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema version %d", ErrInvalidRecord, r.SchemaVersion)
	}
	if err := requireID("runId", r.RunID); err != nil {
		return err
	}
	if err := requireID("producerId", r.ProducerID); err != nil {
		return err
	}
	if r.Sequence == 0 {
		return fmt.Errorf("%w: sequence must start at 1", ErrInvalidRecord)
	}

	return r.validateKind()
}

func (r *Record) validateKind() error {
	switch r.Kind {
	case KindOutput:
		return r.validateOutput()
	case KindInvocation:
		return r.validateInvocation()
	case KindProducer:
		return r.validateProducer()
	case KindCapture:
		return r.validateCapture()
	default:
		return fmt.Errorf("%w: kind %q", ErrInvalidRecord, r.Kind)
	}
}

func (r *Record) validateProducer() error {
	switch r.ProducerState {
	case "", ProducerReserved, ProducerRunning, ProducerTerminal, ProducerUnavailable:
	default:
		return fmt.Errorf("%w: producer state %q", ErrInvalidRecord, r.ProducerState)
	}
	if r.ChildProducerID != "" {
		if err := requireID("childProducerId", r.ChildProducerID); err != nil {
			return err
		}
	}

	return validLocation(r.Location)
}

func (r *Record) validateCapture() error {
	switch r.Condition {
	case ConditionTruncated, ConditionRetentionGap, ConditionWriteError,
		ConditionUnsupportedSource, ConditionSourceUnavailable, ConditionInterrupted,
		ConditionCapacity, ConditionProducerCap, ConditionMalformedRecord:
		return validLocation(r.Location)
	default:
		return fmt.Errorf("%w: condition %q", ErrInvalidRecord, r.Condition)
	}
}

func validLocation(l Location) error {
	switch l {
	case "", LocationHost, LocationProviderHost, LocationContainer:
		return nil
	default:
		return fmt.Errorf("%w: location %q", ErrInvalidRecord, l)
	}
}

func (r *Record) validateOutput() error {
	if err := requireID("invocationId", r.InvocationID); err != nil {
		return err
	}
	switch r.Stream {
	case StreamStdout, StreamStderr, StreamCombined:
	default:
		return fmt.Errorf("%w: stream %q", ErrInvalidRecord, r.Stream)
	}
	if len(r.Text) > MaxChunkBytes {
		return fmt.Errorf("%w: output chunk exceeds %d bytes", ErrInvalidRecord, MaxChunkBytes)
	}
	if !utf8.ValidString(r.Text) {
		return fmt.Errorf("%w: output is not valid UTF-8", ErrInvalidRecord)
	}

	return nil
}

func (r *Record) validateInvocation() error {
	if err := requireID("invocationId", r.InvocationID); err != nil {
		return err
	}
	if r.Hook != "" && !ValidHook(r.Hook) {
		return fmt.Errorf("%w: hook %q", ErrInvalidRecord, r.Hook)
	}
	switch r.State {
	case InvocationPending, InvocationDeferred, InvocationRunning, InvocationSucceeded,
		InvocationFailed, InvocationSkipped, InvocationCanceled, InvocationUnknown:
		return nil
	default:
		return fmt.Errorf("%w: invocation state %q", ErrInvalidRecord, r.State)
	}
}

// SplitText splits text into chunks of at most max bytes without breaking a
// UTF-8 sequence. A trailing fragment without a newline is kept.
func SplitText(text string, maxBytes int) []string {
	if maxBytes <= 0 {
		maxBytes = MaxChunkBytes
	}
	var chunks []string
	for len(text) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if cut == 0 {
			cut = maxBytes
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	if text != "" {
		chunks = append(chunks, text)
	}

	return chunks
}

// ProducerRef is a run manifest entry for one producer.
type ProducerRef struct {
	ProducerID       string        `json:"producerId"`
	ParentProducerID string        `json:"parentProducerId,omitempty"`
	State            ProducerState `json:"state"`
	Location         Location      `json:"location,omitempty"`
}

// RunManifest is the per-run metadata file.
type RunManifest struct {
	SchemaVersion      int           `json:"schemaVersion"`
	RunID              string        `json:"runId"`
	Context            string        `json:"context"`
	WorkspaceUID       *string       `json:"workspaceUid"`
	WorkspaceIDAtStart string        `json:"workspaceIdAtStart,omitempty"`
	Command            string        `json:"command"`
	TaskID             string        `json:"taskId,omitempty"`
	CreatedAt          time.Time     `json:"createdAt"`
	UpdatedAt          time.Time     `json:"updatedAt"`
	CommandState       CommandState  `json:"commandState"`
	CaptureState       CaptureState  `json:"captureState"`
	Conditions         []Condition   `json:"conditions,omitempty"`
	Producers          []ProducerRef `json:"producers,omitempty"`
	DiscoverySealed    bool          `json:"discoverySealed,omitempty"`
}

// RunSpec describes a run at creation. WorkspaceUID may stay empty until the
// workspace is resolved and bound.
type RunSpec struct {
	// RunID joins an existing run created by another process. Empty allocates
	// a new run.
	RunID              string
	Context            string
	WorkspaceUID       string
	WorkspaceIDAtStart string
	Command            string
	TaskID             string
}

// RunSummary is the bounded run description returned by readers.
type RunSummary struct {
	RunID              string       `json:"runId"`
	Context            string       `json:"context"`
	WorkspaceUID       *string      `json:"workspaceUid"`
	WorkspaceIDAtStart string       `json:"workspaceIdAtStart,omitempty"`
	Command            string       `json:"command"`
	TaskID             string       `json:"taskId,omitempty"`
	CreatedAt          time.Time    `json:"createdAt"`
	CommandState       CommandState `json:"commandState"`
	CaptureState       CaptureState `json:"captureState"`
	Conditions         []Condition  `json:"conditions,omitempty"`
}

// Summary returns the bounded view of m.
func (m *RunManifest) Summary() RunSummary {
	return RunSummary{
		RunID:              m.RunID,
		Context:            m.Context,
		WorkspaceUID:       m.WorkspaceUID,
		WorkspaceIDAtStart: m.WorkspaceIDAtStart,
		Command:            m.Command,
		TaskID:             m.TaskID,
		CreatedAt:          m.CreatedAt,
		CommandState:       m.CommandState,
		CaptureState:       m.CaptureState,
		Conditions:         m.Conditions,
	}
}

// Complete reports whether every selected record has been captured: discovery
// is sealed, every producer is terminal and capture has no gaps.
func (m *RunManifest) Complete() bool {
	if !m.DiscoverySealed || m.CaptureState != CaptureComplete {
		return false
	}
	for _, p := range m.Producers {
		if p.State != ProducerTerminal {
			return false
		}
	}

	return true
}

// Active reports whether a writer may still append to the run: discovery is
// open or a producer has not reached a final state.
func (m *RunManifest) Active() bool {
	if !m.DiscoverySealed {
		return true
	}
	for _, p := range m.Producers {
		if p.State == ProducerReserved || p.State == ProducerRunning {
			return true
		}
	}

	return false
}
