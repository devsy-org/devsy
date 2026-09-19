package machinediagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const DefaultLocatorPath = "/run/devsy/agent-daemon.json"

type Locator struct {
	SchemaVersion  int       `json:"schemaVersion"`
	SessionID      string    `json:"sessionId"`
	DiagnosticsDir string    `json:"diagnosticsDir"`
	StateLayout    string    `json:"stateLayout"`
	StartedAt      time.Time `json:"startedAt"`
}

func WriteLocator(
	path string,
	locator Locator,
) error { //nolint:cyclop // atomic locator writes must validate each step.
	if !filepath.IsAbs(path) || !filepath.IsAbs(locator.DiagnosticsDir) {
		return fmt.Errorf("locator paths must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	locator.SchemaVersion = SchemaVersion
	b, err := json.Marshal(locator)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".agent-daemon-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadLocator(path string) (Locator, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is the configured diagnostics locator.
	if err != nil {
		return Locator{}, err
	}
	var locator Locator
	if err := json.Unmarshal(b, &locator); err != nil {
		return Locator{}, err
	}
	if locator.SchemaVersion != SchemaVersion || !filepath.IsAbs(locator.DiagnosticsDir) {
		return Locator{}, fmt.Errorf("invalid diagnostics locator")
	}
	return locator, nil
}

// ReadFromLocator reads the active daemon store without making callers infer
// the service user's home directory or parse a systemd unit.
func ReadFromLocator(
	path, after string,
	limit int,
	interval time.Duration,
	now time.Time,
) ReadResponse { //nolint:revive // the reader API keeps cursor parameters together.
	locator, err := ReadLocator(path)
	if err != nil {
		response := ReadResponse{
			SchemaVersion: SchemaVersion,
			Availability:  AvailabilityNotInitialized,
			ObservedAt:    now.UTC(),
			Freshness:     FreshnessUnknown,
			Cursor:        CursorInfo{State: CursorNone},
		}
		if os.IsPermission(err) {
			response.Availability = AvailabilityPermissionDenied
			response.Error = &DiagnosticError{
				Code:      diagnosticsPermissionDenied,
				Message:   "Access to the remote diagnostics locator was denied.",
				Timestamp: now.UTC(),
			}
		} else if !os.IsNotExist(err) {
			response.Availability = AvailabilityCorrupt
			response.Error = &DiagnosticError{
				Code:      diagnosticsCorrupt,
				Message:   "Remote diagnostics locator could not be read.",
				Timestamp: now.UTC(),
			}
		}
		return response
	}
	return Read(locator.DiagnosticsDir, after, limit, interval, now)
}
