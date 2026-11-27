package audit

import (
	"errors"
	"fmt"
	"slices"
	"syscall"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
)

// WriterConfig sets the envelope fields shared by every event.
type WriterConfig struct {
	Pool          string
	Host          string
	PolicyVersion string
	ConfigVersion string
	Now           func() time.Time
	NewID         func() string
}

// Event supplies the fields specific to one audit event.
type Event struct {
	Type        string
	Session     string
	Actor       envelope.Actor
	TraceID     string
	OperationID string
	Workspace   string
	Data        map[string]any
}

// Writer validates typed events and appends them to the local audit store.
type Writer struct {
	store *Store
	cfg   WriterConfig
}

// NewWriter requires a store and the shared envelope fields.
func NewWriter(store *Store, cfg WriterConfig) (*Writer, error) {
	if store == nil {
		return nil, errors.New("audit writer requires a store")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = envelope.NewID
	}
	return &Writer{store: store, cfg: cfg}, nil
}

// Write appends an event, including local-only events. On error the caller
// must refuse the action. A store sync failure may leave the record on disk.
// The envelope must pass envelope.Validate. The caller keeps e.Data and its
// values unchanged until Write returns, and the configured callbacks must be
// safe for concurrent use.
func (w *Writer) Write(e Event) error {
	if err := checkEvent(e); err != nil {
		return err
	}
	if err := checkCharsets(e); err != nil {
		return err
	}
	if err := checkSession(e, w.cfg.Pool); err != nil {
		return err
	}
	id := w.cfg.NewID()
	if id == "" {
		return fmt.Errorf("write audit event %s: id is empty", e.Type)
	}
	env := w.envelope(e, id)
	// Validation and JSON errors can contain raw event values.
	if err := env.Validate(); err != nil {
		return fmt.Errorf("write audit event %s: validation failed", e.Type)
	}
	line, err := env.MarshalJSON()
	if err != nil {
		return fmt.Errorf("write audit event %s: marshal envelope failed", e.Type)
	}
	if err := w.store.Append(line); err != nil {
		return appendFailure(e.Type, err)
	}
	return nil
}

// appendFailure keeps only a cause that carries no path or event value.
func appendFailure(typ string, err error) error {
	if errors.Is(err, errLockTimeout) {
		return fmt.Errorf("append audit event %s: %w", typ, errLockTimeout)
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return fmt.Errorf("append audit event %s: %w", typ, errno)
	}
	return fmt.Errorf("append audit event %s failed", typ)
}

func checkEvent(e Event) error {
	switch {
	case !slices.Contains(envelope.AuditTypes(), e.Type):
		return errors.New("write audit event: unregistered type")
	case envelope.AuditTypeRequiresSession(e.Type) && e.Session == "":
		return fmt.Errorf("write audit event %s: session is required", e.Type)
	case !e.Actor.Kind.Known():
		return fmt.Errorf("write audit event %s: unknown actor kind", e.Type)
	case e.Actor.ID == "":
		return fmt.Errorf("write audit event %s: actor id is empty", e.Type)
	}
	return nil
}

func (w *Writer) envelope(e Event, id string) envelope.Envelope {
	return envelope.Envelope{
		V:             envelope.Version,
		Kind:          envelope.KindAudit,
		Type:          e.Type,
		ID:            id,
		TS:            envelope.FormatTime(w.cfg.Now()),
		Src:           "pool",
		TraceID:       e.TraceID,
		OperationID:   e.OperationID,
		Workspace:     e.Workspace,
		Session:       e.Session,
		Pool:          w.cfg.Pool,
		Host:          w.cfg.Host,
		PolicyVersion: w.cfg.PolicyVersion,
		ConfigVersion: w.cfg.ConfigVersion,
		Actor:         &e.Actor,
		Data:          e.Data,
	}
}

func (cfg WriterConfig) validate() error {
	switch {
	case cfg.Pool == "":
		return errors.New("audit writer requires pool")
	case cfg.Host == "":
		return errors.New("audit writer requires host")
	case cfg.PolicyVersion == "":
		return errors.New("audit writer requires policyVersion")
	case cfg.ConfigVersion == "":
		return errors.New("audit writer requires configVersion")
	}
	return nil
}
