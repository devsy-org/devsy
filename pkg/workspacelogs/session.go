package workspacelogs

import (
	"context"
	"errors"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
)

// SessionOptions describes the run an entry point records into.
type SessionOptions struct {
	// RunID joins a run allocated earlier (a detached submission or a parent
	// process). Empty allocates a new run.
	RunID string
	// ParentProducerID links this process's producer to the one that
	// launched it.
	ParentProducerID string
	Run              RunSpec
	Location         Location
	Redactor         *secrets.Redactor
}

// Session is one process's handle on a run: the local store, the run, and a
// recorder for this process. A nil Session is valid and does nothing, so
// storage problems never change the command being recorded.
type Session struct {
	Store    *Store
	Recorder *Recorder
	RunID    string
	started  time.Time
}

// StartSession opens the default store, creates or joins the run and starts a
// recorder. It returns nil when history cannot be recorded.
func StartSession(opts SessionOptions) *Session {
	store, err := OpenDefault()
	if err != nil {
		return nil
	}

	return startSessionIn(store, opts)
}

func startSessionIn(store *Store, opts SessionOptions) *Session {
	opts.Run.RunID = opts.RunID
	m, err := store.CreateRun(opts.Run)
	if err != nil {
		return nil
	}
	rec, err := NewRecorder(RecorderOptions{
		Backend:          store,
		RunID:            m.RunID,
		Location:         opts.Location,
		ParentProducerID: opts.ParentProducerID,
		Redactor:         opts.Redactor,
	})
	if err != nil {
		return nil
	}

	return &Session{Store: store, Recorder: rec, RunID: m.RunID, started: time.Now()}
}

// Context returns ctx carrying this session's recorder.
func (s *Session) Context(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}

	return WithRecorder(ctx, s.Recorder)
}

// ProducerID returns this process's producer ID, or empty without a session.
func (s *Session) ProducerID() string {
	if s == nil {
		return ""
	}

	return s.Recorder.ProducerID()
}

// BindWorkspace binds the resolved workspace UID to the run.
func (s *Session) BindWorkspace(uid, idAtStart string) {
	if s == nil || uid == "" {
		return
	}
	_ = s.Store.BindWorkspace(s.RunID, uid, idAtStart)
}

// Finish records the command outcome and closes this process's producer. Pass
// sealDiscovery only from the process that schedules no further producers.
func (s *Session) Finish(err error, sealDiscovery bool) {
	if s == nil {
		return
	}
	s.Recorder.Close(ProducerTerminal, sealDiscovery)
	_ = s.Store.SetCommandState(s.RunID, commandStateFor(err))
}

func commandStateFor(err error) CommandState {
	switch {
	case err == nil:
		return CommandSucceeded
	case errors.Is(err, context.Canceled):
		return CommandCanceled
	default:
		return CommandFailed
	}
}
