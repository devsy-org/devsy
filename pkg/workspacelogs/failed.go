package workspacelogs

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

// latestStates returns each invocation's latest state and the order the
// invocations first appeared in.
func (s *Store) latestStates(m *RunManifest) (map[string]InvocationState, []string, error) {
	state := map[string]InvocationState{}
	var order []string
	for _, p := range m.Producers {
		err := s.eachRecord(m.RunID, p.ProducerID, 1, func(rec *Record) bool {
			if rec == nil || rec.Kind != KindInvocation {
				return true
			}
			if _, seen := state[rec.InvocationID]; !seen {
				order = append(order, rec.InvocationID)
			}
			state[rec.InvocationID] = rec.State

			return true
		})
		if err != nil {
			return nil, nil, err
		}
	}

	return state, order, nil
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
