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
	state := map[string]InvocationState{}
	var order []string
	for _, p := range m.Producers {
		err = s.eachRecord(runID, p.ProducerID, 1, func(rec *Record) bool {
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
			return nil, err
		}
	}
	var failed []string
	for _, id := range order {
		if state[id] == InvocationFailed && len(failed) < MaxProducers {
			failed = append(failed, id)
		}
	}

	return failed, nil
}
