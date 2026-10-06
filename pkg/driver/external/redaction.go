package external

import (
	"sync"

	"github.com/devsy-org/devsy/pkg/secrets"
)

// Shared through a pointer so operation snapshots never copy a live mutex.
// Keep prior values for the host's lifetime: retries and backend diagnostics
// can still contain secrets from an earlier or partially failed RunImage.
type workspaceRedactions struct {
	mu         sync.Mutex
	workspaces map[string]*workspaceRedaction
}

type workspaceRedaction struct {
	mu       sync.Mutex
	values   map[string]struct{}
	redactor *secrets.Redactor
}

func (h *Host) forWorkspace(workspaceID string, environment map[string]string) *Host {
	h.redactions.mu.Lock()
	workspace := h.redactions.workspaces[workspaceID]
	if workspace == nil && len(environment) > 0 {
		workspace = &workspaceRedaction{values: make(map[string]struct{}), redactor: h.redactor}
		h.redactions.workspaces[workspaceID] = workspace
	}
	h.redactions.mu.Unlock()
	operation := *h
	if workspace != nil {
		operation.redactor = workspace.snapshot(h.redactor, environment)
	}
	return &operation
}

func (w *workspaceRedaction) snapshot(
	base *secrets.Redactor,
	environment map[string]string,
) *secrets.Redactor {
	w.mu.Lock()
	defer w.mu.Unlock()
	changed := false
	for _, value := range environment {
		if _, known := w.values[value]; value != "" && !known {
			w.values[value] = struct{}{}
			changed = true
		}
	}
	if changed {
		entries := make([]string, 0, len(w.values))
		for value := range w.values {
			entries = append(entries, "WORKSPACE_VALUE="+value)
		}
		w.redactor = secrets.Combine(base, secrets.NewRedactor(entries))
	}
	return w.redactor
}
