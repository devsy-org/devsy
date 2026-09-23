package machinediagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const DefaultLocatorPath = DefaultRuntimeDir + "/" + RuntimeLocatorFileName

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
) error {
	if !filepath.IsAbs(path) || !filepath.IsAbs(locator.DiagnosticsDir) {
		return fmt.Errorf("locator paths must be absolute")
	}
	if err := ensureRuntimeDirForPath(path); err != nil {
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
	err = writeLocatorContents(f, b)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeLocatorContents(f *os.File, b []byte) error {
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	return f.Sync()
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
func ReadFromLocator(path string, options ReadOptions) ReadResponse {
	now := options.Now
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
	return Read(locator.DiagnosticsDir, options)
}

// ReadActive reads the highest-priority active daemon locator. A missing or
// inaccessible system locator permits trying the per-user fallback; a corrupt
// system locator is authoritative and is never masked by stale fallback data.
func ReadActive(options ReadOptions, userCacheDir func() (string, error)) (ReadResponse, error) {
	system := readLocatorCandidate(DefaultLocatorPath, options)
	if system.state == locatorCandidateSuccess || system.state == locatorCandidateCorrupt {
		return system.response, nil
	}
	userPaths, err := UserRuntimePaths(userCacheDir)
	if err != nil {
		return ReadResponse{}, err
	}
	user := readLocatorCandidate(userPaths.LocatorPath, options)
	if user.state == locatorCandidateSuccess {
		return user.response, nil
	}
	if system.state == locatorCandidatePermission {
		return ReadFromLocator(DefaultLocatorPath, options), nil
	}
	if user.state == locatorCandidateCorrupt {
		return user.response, nil
	}
	if system.state == locatorCandidateStale {
		return system.response, nil
	}
	return ReadFromLocator(DefaultLocatorPath, options), nil
}

func ActiveLocatorCandidates(userCacheDir func() (string, error)) ([]string, error) {
	paths := []string{DefaultLocatorPath}
	userPaths, err := UserRuntimePaths(userCacheDir)
	if err != nil {
		return nil, err
	}
	if userPaths.LocatorPath != DefaultLocatorPath {
		paths = append(paths, userPaths.LocatorPath)
	}
	return paths, nil
}

func readActiveFromCandidates(paths []string, options ReadOptions) ReadResponse {
	var permissionResponse ReadResponse
	var staleResponse ReadResponse
	for i, path := range paths {
		candidate := readLocatorCandidate(path, options)
		response, done, updatedPermission := applyLocatorCandidate(
			candidate,
			i,
			permissionResponse,
		)
		if done {
			return response
		}
		permissionResponse = updatedPermission
		if candidate.state == locatorCandidateStale && staleResponse.Availability == "" {
			staleResponse = candidate.response
		}
	}
	if permissionResponse.Availability != "" {
		return permissionResponse
	}
	if staleResponse.Availability != "" {
		return staleResponse
	}
	return ReadFromLocator(DefaultLocatorPath, options)
}

func applyLocatorCandidate(
	candidate locatorCandidate,
	index int,
	permissionResponse ReadResponse,
) (ReadResponse, bool, ReadResponse) {
	switch candidate.state {
	case locatorCandidateSuccess:
		return candidate.response, true, permissionResponse
	case locatorCandidateCorrupt:
		if index == 0 || permissionResponse.Availability == "" {
			return candidate.response, true, permissionResponse
		}
		return permissionResponse, true, permissionResponse
	case locatorCandidatePermission:
		if permissionResponse.Availability == "" {
			permissionResponse = candidate.response
		}
	case locatorCandidateStale:
		return ReadResponse{}, false, permissionResponse
	}
	return ReadResponse{}, false, permissionResponse
}

type locatorCandidateState uint8

const (
	locatorCandidateMissing locatorCandidateState = iota
	locatorCandidatePermission
	locatorCandidateCorrupt
	locatorCandidateSuccess
	locatorCandidateStale
)

type locatorCandidate struct {
	response ReadResponse
	state    locatorCandidateState
}

func readLocatorCandidate(path string, options ReadOptions) locatorCandidate {
	locator, err := ReadLocator(path)
	if err != nil {
		switch {
		case os.IsNotExist(err):
			return locatorCandidate{state: locatorCandidateMissing}
		case os.IsPermission(err):
			return locatorCandidate{
				response: ReadFromLocator(path, options),
				state:    locatorCandidatePermission,
			}
		default:
			return locatorCandidate{
				response: ReadFromLocator(path, options),
				state:    locatorCandidateCorrupt,
			}
		}
	}
	return classifyLocatorResponse(Read(locator.DiagnosticsDir, options))
}

func classifyLocatorResponse(response ReadResponse) locatorCandidate {
	if response.Availability == AvailabilityAvailable && response.Status != nil {
		if response.Status.State == DaemonStopping || response.Freshness == FreshnessStale {
			return locatorCandidate{response: response, state: locatorCandidateStale}
		}
		return locatorCandidate{response: response, state: locatorCandidateSuccess}
	}
	if response.Availability == AvailabilityNotInitialized {
		return locatorCandidate{state: locatorCandidateMissing}
	}
	if response.Availability == AvailabilityPermissionDenied {
		return locatorCandidate{response: response, state: locatorCandidatePermission}
	}
	return locatorCandidate{response: response, state: locatorCandidateCorrupt}
}
