package opener

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	pkglog "github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/shirou/gopsutil/v4/process"
)

// TunnelStateFileName is the filename used to record the browser tunnel state
// inside the workspace directory.
const TunnelStateFileName = "tunnel.json"

// TunnelLockFileName is the filename used to serialize concurrent attempts to
// start the browser tunnel for a workspace.
const TunnelLockFileName = "tunnel.lock"

// TunnelLogFileName is the filename used for the detached browser tunnel
// helper's stdout/stderr log.
const TunnelLogFileName = "tunnel.log"

// LabelVSCodeBrowser is the TunnelState.Label value used for the openvscode
// (VS Code Browser) tunnel. Exposed so tests can reference it without
// duplicating the literal.
const LabelVSCodeBrowser = "vscode"

// LabelCodeServer is the TunnelState.Label value used for the code-server
// (coder.com) browser-IDE tunnel.
const LabelCodeServer = "code-server"

// TunnelState describes a running browser tunnel for a workspace.
//
// CreateTime is the helper process's creation timestamp in milliseconds
// since epoch. It pairs with PID to detect PID reuse: if the process at PID
// has a different CreateTime than the recorded value, the helper is gone
// and a foreign process now occupies that PID.
type TunnelState struct {
	PID        int    `json:"pid"`
	CreateTime int64  `json:"createTime"`
	TargetURL  string `json:"targetUrl"`
	Label      string `json:"label,omitempty"`
}

// TunnelStateFilePath returns the path used to store the browser tunnel state
// file for the given workspace context and workspace ID.
func TunnelStateFilePath(contextName, workspaceID string) (string, error) {
	workspaceDir, err := provider.GetWorkspaceDir(contextName, workspaceID)
	if err != nil {
		return "", fmt.Errorf("get workspace dir: %w", err)
	}
	return filepath.Join(workspaceDir, TunnelStateFileName), nil
}

// tunnelLogFilePath returns the path used to store the detached browser tunnel
// helper's log file for the given workspace context and workspace ID.
func tunnelLogFilePath(contextName, workspaceID string) (string, error) {
	workspaceDir, err := provider.GetWorkspaceDir(contextName, workspaceID)
	if err != nil {
		return "", fmt.Errorf("get workspace dir: %w", err)
	}
	return filepath.Join(workspaceDir, TunnelLogFileName), nil
}

// ReadTunnelState reads and returns the recorded tunnel state for a workspace.
// Returns (nil, nil) if no state file exists.
func ReadTunnelState(contextName, workspaceID string) (*TunnelState, error) {
	statePath, err := TunnelStateFilePath(contextName, workspaceID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(statePath) // #nosec G304: path derived from devsy config
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var state TunnelState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse tunnel state: %w", err)
	}
	return &state, nil
}

// WriteTunnelState persists the tunnel state for a workspace.
func WriteTunnelState(contextName, workspaceID string, state TunnelState) error {
	statePath, err := TunnelStateFilePath(contextName, workspaceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal tunnel state: %w", err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		return fmt.Errorf("write tunnel state file: %w", err)
	}
	return nil
}

// helperCreateTime returns the creation timestamp (milliseconds since
// epoch) of the process with the given PID. It's used right after spawning
// the helper so a PID+CreateTime identity can be persisted in the state
// file.
func helperCreateTime(pid int) (int64, error) {
	if pid <= 0 || pid > math.MaxInt32 {
		return 0, fmt.Errorf("pid %d out of int32 range", pid)
	}
	//nolint:gosec // pid is bounds-checked above
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, fmt.Errorf("lookup process: %w", err)
	}
	t, err := p.CreateTime()
	if err != nil {
		return 0, fmt.Errorf("read process create time: %w", err)
	}
	return t, nil
}

// helperMatchesState reports whether the process at state.PID matches the
// helper as recorded by comparing its current creation time against
// state.CreateTime. Returns false if the process no longer exists, the
// creation time can't be read, or the creation time differs (PID reuse).
func helperMatchesState(state *TunnelState) bool {
	if state == nil || state.PID <= 0 || state.PID > math.MaxInt32 {
		return false
	}
	//nolint:gosec // PID is bounds-checked above
	p, err := process.NewProcess(int32(state.PID))
	if err != nil {
		if errors.Is(err, process.ErrorProcessNotRunning) {
			return false
		}
		// Unexpected error (permission denied on foreign-UID Linux or
		// Windows, /proc not mounted, gopsutil parse error). Treat as
		// "process is probably still the recorded helper" to avoid a
		// silent respawn that would orphan a live helper still holding
		// the port.
		pkglog.Warnf(
			"tunnel helper pid %d: identity check failed (%v); assuming live to avoid orphan",
			state.PID, err,
		)
		return true
	}
	createTime, err := p.CreateTime()
	if err != nil {
		pkglog.Warnf(
			"tunnel helper pid %d: read create time failed (%v); assuming live to avoid orphan",
			state.PID, err,
		)
		return true
	}
	return createTime == state.CreateTime
}

// loadLiveTunnelState reads the tunnel state file and returns it only if the
// recorded PID is still the owned helper (PID alive AND CreateTime matches).
// If the state file is missing, unreadable, or the PID is no longer owned
// (dead, or reused by another process), the stale state file is removed and
// nil is returned.
func loadLiveTunnelState(contextName, workspaceID, statePath string) *TunnelState {
	state, err := ReadTunnelState(contextName, workspaceID)
	if err != nil {
		pkglog.Debugf("read tunnel state file: %v", err)
		_ = os.Remove(statePath)
		return nil
	}
	if state == nil {
		_ = os.Remove(statePath)
		return nil
	}
	if !helperMatchesState(state) {
		_ = os.Remove(statePath)
		return nil
	}
	return state
}
