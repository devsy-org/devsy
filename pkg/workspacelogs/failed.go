package workspacelogs

import "time"

// Keys under which a failed command carries run identity in its error context.
const (
	ContextRunID              = "runId"
	ContextFailedInvocationID = "failedInvocationId"
)

// FailedInvocations returns the IDs of invocations whose latest recorded state
// is failed, in order of first appearance. At most MaxProducers IDs are
// returned; callers only use an ID when exactly one invocation failed.
func (s *Store) FailedInvocations(runID string) ([]string, error) {
	m, err := s.Manifest(runID)
	if err != nil {
		return nil, err
	}
	state, order, err := s.latestStates(m)
	if err != nil {
		return nil, err
	}
	var failed []string
	for _, id := range order {
		if state[id] == InvocationFailed && len(failed) < MaxProducers {
			failed = append(failed, id)
		}
	}

	return failed, nil
}

type transitionMark struct {
	producerID string
	sequence   uint64
	rank       int
	time       time.Time
}

// supersedes reports whether next replaces prior. Sequences only compare
// inside one producer. Across producers a result is never replaced by an
// earlier phase, and equal phases fall back to record time.
func (next transitionMark) supersedes(prior transitionMark) bool {
	if prior.producerID == next.producerID {
		return next.sequence >= prior.sequence
	}
	if prior.rank != next.rank {
		return next.rank > prior.rank
	}

	return !next.time.Before(prior.time)
}

func (s InvocationState) rank() int {
	switch s {
	case InvocationPending:
		return 0
	case InvocationDeferred:
		return 1
	case InvocationRunning:
		return 2
	case InvocationSkipped:
		return 3
	default:
		return 4
	}
}

func (s InvocationState) inFlight() bool {
	return s == InvocationPending || s == InvocationDeferred || s == InvocationRunning
}

type stateFold struct {
	state  map[string]InvocationState
	latest map[string]transitionMark
	order  []string
}

func (f *stateFold) add(producerID string, rec *Record) {
	next := transitionMark{producerID, rec.Sequence, rec.State.rank(), rec.Time}
	prior, seen := f.latest[rec.InvocationID]
	if !seen {
		f.order = append(f.order, rec.InvocationID)
	} else if !next.supersedes(prior) {
		return
	}
	f.latest[rec.InvocationID] = next
	f.state[rec.InvocationID] = rec.State
}

// latestStates returns each invocation's latest state and the order the
// invocations first appeared in. Once no producer can still write, a state
// that never reached a result is reported as unknown.
func (s *Store) latestStates(m *RunManifest) (map[string]InvocationState, []string, error) {
	fold := &stateFold{
		state:  map[string]InvocationState{},
		latest: map[string]transitionMark{},
	}
	for _, p := range m.Producers {
		err := s.eachRecord(m.RunID, p.ProducerID, 1, func(rec *Record) bool {
			if rec != nil && rec.Kind == KindInvocation {
				fold.add(p.ProducerID, rec)
			}

			return true
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if !m.Active() {
		for id, st := range fold.state {
			if st.inFlight() {
				fold.state[id] = InvocationUnknown
			}
		}
	}

	return fold.state, fold.order, nil
}

// FailureContext returns the error context that identifies a failed run: the
// run ID, plus the failed invocation when exactly one invocation failed.
// Lookup problems yield whatever identity is known.
func (s *Store) FailureContext(runID string) map[string]string {
	if runID == "" {
		return nil
	}
	values := map[string]string{ContextRunID: runID}
	if s == nil {
		return values
	}
	if failed, err := s.FailedInvocations(runID); err == nil && len(failed) == 1 {
		values[ContextFailedInvocationID] = failed[0]
	}

	return values
}

// FailureContext is the Session form of Store.FailureContext. A nil Session
// has no history to point at.
func (s *Session) FailureContext() map[string]string {
	if s == nil {
		return nil
	}

	return s.Store.FailureContext(s.RunID)
}
