package task

import "github.com/devsy-org/devsy/pkg/workspacelogs"

// runFailureContext returns the run identity to attach to a failed task's
// error context. Lookup problems are not task failures, so they yield whatever
// identity is known.
func runFailureContext(runID string) map[string]string {
	if runID == "" {
		return nil
	}
	store, err := workspacelogs.OpenDefault()
	if err != nil {
		return map[string]string{workspacelogs.ContextRunID: runID}
	}

	return store.FailureContext(runID)
}

// withRunContext adds run identity to an error context without replacing keys
// the failure already carries.
func withRunContext(base, run map[string]string) map[string]string {
	if len(run) == 0 {
		return base
	}
	if base == nil {
		base = make(map[string]string, len(run))
	}
	for k, v := range run {
		if _, exists := base[k]; !exists {
			base[k] = v
		}
	}

	return base
}
