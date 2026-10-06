package external

import (
	"sync"

	"github.com/devsy-org/devsy/pkg/secrets"
)

// Shared through a pointer so operation snapshots never copy a live mutex.
// Keep prior values for the host's lifetime: retries and backend diagnostics
// can still contain secrets from an earlier or partially failed RunImage.
type workspaceRedactions struct {
	mu     sync.Mutex
	values map[string]map[string]struct{}
}

func (h *Host) forWorkspace(workspaceID string, environment map[string]string) *Host {
	h.redactions.mu.Lock()
	defer h.redactions.mu.Unlock()
	known := h.redactions.values[workspaceID]
	if known == nil && len(environment) > 0 {
		known = make(map[string]struct{})
		h.redactions.values[workspaceID] = known
	}
	for _, value := range environment {
		if value != "" {
			known[value] = struct{}{}
		}
	}
	entries := make([]string, 0, len(known))
	for value := range known {
		entries = append(entries, "WORKSPACE_VALUE="+value)
	}
	operation := *h
	operation.redactor = secrets.Combine(h.redactor, secrets.NewRedactor(entries))
	return &operation
}
