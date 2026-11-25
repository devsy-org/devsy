package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
)

// DataValidator checks the audit data for a registered event type.
type DataValidator func(typ string, data map[string]any) error

// WriterConfig sets the envelope fields shared by every event.
type WriterConfig struct {
	Pool          string
	Host          string
	PolicyVersion string
	ConfigVersion string
	Validate      DataValidator
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

// NewWriter requires a store, shared envelope fields and a data validator.
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
func (w *Writer) Write(e Event) error {
	switch {
	case !slices.Contains(envelope.AuditTypes(), e.Type):
		return errors.New("write audit event: unregistered type")
	case envelope.AuditTypeRequiresSession(e.Type) && e.Session == "":
		return fmt.Errorf("write audit event %s: session is required", e.Type)
	case !e.Actor.Kind.Known():
		return fmt.Errorf("write audit event %s: unknown actor kind", e.Type)
	}
	// Validator and JSON errors can contain raw event values.
	if err := w.cfg.Validate(e.Type, e.Data); err != nil {
		return fmt.Errorf("write audit event %s: data validation failed", e.Type)
	}
	line, err := json.Marshal(w.envelope(e))
	if err != nil {
		return fmt.Errorf("write audit event %s: marshal envelope failed", e.Type)
	}
	if err := w.store.Append(line); err != nil {
		return fmt.Errorf("append audit event %s: %w", e.Type, err)
	}
	return nil
}

func (w *Writer) envelope(e Event) envelope.Envelope {
	return envelope.Envelope{
		V:             envelope.Version,
		Kind:          envelope.KindAudit,
		Type:          e.Type,
		ID:            w.cfg.NewID(),
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
	case cfg.Validate == nil:
		return errors.New("audit writer requires a data validator")
	}
	return nil
}
