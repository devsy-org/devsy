package opener

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	pkglog "github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/open"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"github.com/gofrs/flock"
)

// KillBrowserTunnel terminates the detached browser tunnel for a workspace
// (if any) and removes its state file. Missing files and dead PIDs are
// tolerated silently. This is safe to call from any workspace teardown or
// recreate path.
func KillBrowserTunnel(contextName, workspaceID string) {
	statePath, err := TunnelStateFilePath(contextName, workspaceID)
	if err != nil {
		return
	}
	// If the workspace dir is already gone, there's nothing to lock on and
	// nothing to kill — skip silently rather than recreating the dir just
	// for a lock file.
	if _, statErr := os.Stat(filepath.Dir(statePath)); statErr != nil {
		return
	}
	// Serialize against startDetachedBrowserTunnel so a concurrent `devsy up`
	// can't reuse a tunnel marked for SIGTERM, or have its freshly written
	// state file removed mid-spawn.
	unlock, lockErr := acquireTunnelLock(contextName, workspaceID)
	if lockErr != nil {
		pkglog.Warnf(
			"could not acquire tunnel lock for workspace %s teardown (helper may still be running): %v",
			workspaceID,
			lockErr,
		)
		return
	}
	defer unlock()

	state := loadLiveTunnelState(contextName, workspaceID, statePath)
	if state == nil {
		// loadLiveTunnelState already removed the stale file when the PID
		// is no longer owned by this caller (dead, or reused by an unrelated
		// process), avoiding the risk of SIGTERMing a foreign process here.
		return
	}

	terminateTunnelProcess(state.PID)
	_ = os.Remove(statePath)
}

// terminateTunnelProcess SIGTERMs the helper, waits briefly, and SIGKILLs if
// it hasn't exited. A short post-SIGKILL wait avoids a freshly-spawned
// replacement racing the dying helper for the same ports.
func terminateTunnelProcess(pid int) {
	process, _ := os.FindProcess(pid)
	signalTunnel(process, pid, syscall.SIGTERM, "signal tunnel pid %d: %v")

	if waitForExit(pid, 2*time.Second) {
		return
	}
	if process != nil {
		if killErr := process.Kill(); killErr != nil &&
			!errors.Is(killErr, os.ErrProcessDone) {
			pkglog.Debugf("kill tunnel pid %d: %v", pid, killErr)
		}
	}
	if !waitForExit(pid, 500*time.Millisecond) {
		pkglog.Warnf(
			"tunnel helper pid %d did not exit after SIGKILL; removing state file anyway",
			pid,
		)
	}
}

// signalTunnel sends sig to process and logs the error (unless the process
// has already exited). debugFmt must contain "%d %v" verbs for pid and err.
func signalTunnel(process *os.Process, pid int, sig os.Signal, debugFmt string) {
	if process == nil {
		return
	}
	if err := process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		pkglog.Debugf(debugFmt, pid, err)
	}
}

// waitForExit polls until the process with the given PID is no longer alive
// or the timeout elapses. Returns true if the process exited.
func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !isProcessAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// inheritedListenerSetup bundles the result of prepareInheritedListeners so
// the function stays within revive's function-result-limit.
type inheritedListenerSetup struct {
	Files   []*os.File
	Args    []string
	Cleanup func()
}

// browserIDEInvocation bundles the per-call IDE-specific knobs that aren't
// part of the lower-level tunnel parameters.
type browserIDEInvocation struct {
	Label       string // e.g. "vscode", "jupyter", "rstudio" — used in user-facing log lines
	OpenBrowser bool   // whether to launch a host browser pointing at TargetURL
}

// startDetachedBrowserTunnel spawns `devsy helper browser-tunnel ...` as a
// detached background process so the CLI can return to the prompt while the
// tunnel remains running.
//
// If params.Client is a DaemonClient (pro mode) this falls back to running
// the tunnel inline because the daemon already runs out-of-process.
//
// If a tunnel is already running for this workspace, no new process is
// spawned; the existing tunnel is reused.
func startDetachedBrowserTunnel(
	ctx context.Context,
	params IDEParams,
	tunnelParams tunnel.BrowserTunnelParams,
	inv browserIDEInvocation,
) error {
	label := inv.Label
	openBrowser := inv.OpenBrowser
	if _, ok := params.Client.(client.DaemonClient); ok {
		return tunnel.StartBrowserTunnel(ctx, tunnelParams)
	}

	contextName := params.Client.Context()
	workspaceID := params.Client.Workspace()

	// Serialize concurrent attempts (e.g. parallel `devsy up`) to avoid
	// orphaning helper processes by racing on the state file.
	unlock, err := acquireTunnelLock(contextName, workspaceID)
	if err != nil {
		return fmt.Errorf(
			"acquire tunnel lock (refusing to spawn unlocked to avoid orphan helpers): %w",
			err,
		)
	}
	defer unlock()

	// Reuse an existing live tunnel if present; clear stale state otherwise.
	if tryReuseExistingTunnel(ctx, contextName, workspaceID, inv) {
		return nil
	}

	pid, logLocation, err := spawnTunnelHelper(contextName, workspaceID, tunnelParams, label)
	if err != nil {
		return err
	}

	pkglog.Infof(
		"%s browser tunnel running in background (PID %d). Logs: %s. "+
			"Run 'devsy stop %s' to terminate.",
		label, pid, logLocation, workspaceID,
	)

	if openBrowser {
		go openBrowserAsync(ctx, tunnelParams.TargetURL)
	}

	return nil
}

// spawnTunnelHelper locates the current executable, builds the helper
// argument list, pre-binds host listeners (where supported), spawns the
// detached helper process, and records its PID in the tunnel state file.
// It returns the spawned helper PID and the log location that should be
// reported to the user.
func spawnTunnelHelper(
	contextName, workspaceID string,
	tunnelParams tunnel.BrowserTunnelParams,
	label string,
) (int, string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return 0, "", fmt.Errorf("locate executable: %w", err)
	}

	args := buildHelperArgs(contextName, workspaceID, tunnelParams)

	// Pre-bind the host listeners in the parent so the chosen ports are
	// truly reserved before the helper forks. On Windows this is a no-op
	// (os/exec ExtraFiles is unsupported there).
	setup, lerr := prepareInheritedListeners(tunnelParams.ExtraPorts)
	if lerr != nil {
		// EADDRINUSE means the port got stolen between probe and bind —
		// expected and recoverable via the legacy fallback path. Anything
		// else (FD exhaustion, permission denied, transient syscall
		// failure) deserves a Warn so the silent regression to the racy
		// legacy path is visible.
		if errors.Is(lerr, syscall.EADDRINUSE) {
			pkglog.Debugf(
				"inherit-listener: port stolen between probe and bind, falling back: %v",
				lerr,
			)
		} else {
			pkglog.Warnf(
				"inherit-listener preparation failed (falling back to legacy racy port-binding path): %v",
				lerr,
			)
		}
		setup = inheritedListenerSetup{Cleanup: func() {}}
	}
	args = append(args, setup.Args...)

	//nolint:gosec // execPath is the current binary, arguments are controlled
	cmd := exec.Command(execPath, args...)
	setDetachedProcAttrs(cmd)
	cmd.ExtraFiles = setup.Files

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, "", fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdin = devNull

	logFile, logPath := openTunnelLogFile(contextName, workspaceID)
	if logFile != nil {
		defer func() { _ = logFile.Close() }()
	}
	attachHelperStdio(cmd, logFile, devNull)

	if err := cmd.Start(); err != nil {
		setup.Cleanup()
		return 0, "", fmt.Errorf("start browser tunnel: %w", err)
	}
	// Child has inherited duplicates; close the parent's listener+file
	// handles so only the child holds the port.
	setup.Cleanup()

	pid := cmd.Process.Pid
	if err := recordHelperState(cmd.Process, pid, recordHelperStateOpts{
		ContextName: contextName,
		WorkspaceID: workspaceID,
		TargetURL:   tunnelParams.TargetURL,
		Label:       label,
	}); err != nil {
		return 0, "", err
	}

	logLocation := logPath
	if logFile == nil {
		logLocation = os.DevNull
	}
	return pid, logLocation, nil
}

// recordHelperStateOpts bundles workspace identity + helper invocation metadata
// that recordHelperState persists to tunnel.json.
type recordHelperStateOpts struct {
	ContextName string
	WorkspaceID string
	TargetURL   string
	Label       string
}

// recordHelperState captures the helper's process identity (PID +
// CreateTime) and persists it to tunnel.json. On any failure the helper is
// killed to avoid leaving an un-identifiable orphan.
func recordHelperState(proc *os.Process, pid int, opts recordHelperStateOpts) error {
	createTime, err := helperCreateTime(pid)
	if err != nil {
		pkglog.Warnf(
			"identify tunnel helper pid %d (killing to avoid un-identifiable state): %v",
			pid,
			err,
		)
		_ = proc.Kill()
		_ = proc.Release()
		return fmt.Errorf("identify helper pid %d: %w", pid, err)
	}
	state := TunnelState{
		PID:        pid,
		CreateTime: createTime,
		TargetURL:  opts.TargetURL,
		Label:      opts.Label,
	}
	if err := WriteTunnelState(opts.ContextName, opts.WorkspaceID, state); err != nil {
		_ = proc.Kill()
		_ = proc.Release()
		return fmt.Errorf(
			"write tunnel state file (helper PID %d killed to avoid orphan): %w",
			pid,
			err,
		)
	}
	_ = proc.Release()
	return nil
}

// attachHelperStdio wires the helper command's stdout/stderr to logFile if
// available, otherwise falls back to devNull.
func attachHelperStdio(cmd *exec.Cmd, logFile, devNull *os.File) {
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		return
	}
	cmd.Stdout = devNull
	cmd.Stderr = devNull
}

// buildHelperArgs returns the CLI args for the detached browser-tunnel helper.
func buildHelperArgs(
	contextName, workspaceID string,
	tunnelParams tunnel.BrowserTunnelParams,
) []string {
	args := []string{
		"helper", "browser-tunnel",
		"--context", contextName,
		"--workspace", workspaceID,
		"--target-url", tunnelParams.TargetURL,
		"--auth-sock-id", tunnelParams.AuthSockID,
		"--user", tunnelParams.User,
		"--git-ssh-signing-key", tunnelParams.GitSSHSigningKey,
	}
	if tunnelParams.ForwardPorts {
		args = append(args, "--forward-ports")
	}
	for _, p := range tunnelParams.ExtraPorts {
		args = append(args, "--extra-ports", p)
	}
	if pkglog.DebugEnabled() {
		args = append(args, "--debug")
	}
	return args
}

// openTunnelLogFile opens (truncate+create) the per-workspace tunnel log file.
// Returns nil file if the log path could not be determined or opened; callers
// should fall back to /dev/null for stdout/stderr in that case.
func openTunnelLogFile(contextName, workspaceID string) (*os.File, string) {
	logPath, logErr := tunnelLogFilePath(contextName, workspaceID)
	if logErr != nil {
		pkglog.Warnf("open tunnel log file, falling back to /dev/null: %v", logErr)
		return nil, ""
	}
	if mkErr := os.MkdirAll(filepath.Dir(logPath), 0o700); mkErr != nil {
		pkglog.Warnf("create workspace dir for tunnel log: %v", mkErr)
		return nil, logPath
	}
	// #nosec G304 -- path derived from devsy config
	logFile, openErr := os.OpenFile(
		logPath,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
		0o600,
	)
	if openErr != nil {
		pkglog.Warnf("open tunnel log file, falling back to /dev/null: %v", openErr)
		return nil, logPath
	}
	return logFile, logPath
}

// openBrowserAsync opens a URL in the browser without blocking the caller.
//
// Uses context.WithoutCancel so values from the caller's ctx are preserved
// but cancellation isn't propagated: `devsy up` exits as soon as the detached
// helper is spawned, and tying the browser-open retries to the up ctx would
// cancel them prematurely.
//
// The attempt is additionally bounded with a 30s timeout: open.Open retries
// every 1s until its context is done, which on a broken URL or missing
// browser would otherwise loop forever. 30s is generous enough to let the OS
// launch a real browser but short enough to avoid leaking a goroutine.
func openBrowserAsync(ctx context.Context, url string) {
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := open.Open(tctx, url); err != nil {
		pkglog.Warnf(
			"could not open browser automatically (%v); open this URL manually: %s",
			err,
			url,
		)
	}
}

// tryReuseExistingTunnel reuses an already-running tunnel helper for the
// workspace, if any. It returns true if an existing live tunnel was found and
// reused (in which case the caller should not spawn a new helper). If state
// exists but the recorded PID is dead, the stale state file is removed and
// false is returned so the caller can proceed with a fresh spawn.
func tryReuseExistingTunnel(
	ctx context.Context,
	contextName, workspaceID string,
	inv browserIDEInvocation,
) bool {
	existing, _ := ReadTunnelState(contextName, workspaceID)
	if existing == nil {
		return false
	}
	if helperMatchesState(existing) {
		pkglog.Infof(
			"%s browser tunnel already running (PID %d). Reusing existing tunnel at %s",
			inv.Label, existing.PID, existing.TargetURL,
		)
		if inv.OpenBrowser {
			go openBrowserAsync(ctx, existing.TargetURL)
		}
		return true
	}
	if statePath, err := TunnelStateFilePath(contextName, workspaceID); err == nil {
		_ = os.Remove(statePath)
	}
	return false
}

// acquireTunnelLock takes an exclusive file lock that serializes concurrent
// attempts to start (or reuse) the browser tunnel for a workspace. The
// returned function releases the lock.
func acquireTunnelLock(contextName, workspaceID string) (func(), error) {
	workspaceDir, err := provider.GetWorkspaceDir(contextName, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("get workspace dir: %w", err)
	}
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace dir: %w", err)
	}
	lockPath := filepath.Join(workspaceDir, TunnelLockFileName)
	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("acquire tunnel lock: %w", err)
	}
	return func() {
		if err := lock.Unlock(); err != nil {
			pkglog.Debugf("release tunnel lock: %v", err)
		}
	}, nil
}
