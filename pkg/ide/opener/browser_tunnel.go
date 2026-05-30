package opener

import (
	"context"
	"errors"
	"fmt"
	"net"
	neturl "net/url"
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
	"k8s.io/apimachinery/pkg/util/wait"
)

// browserProbeBudget bounds how long openBrowserWhenReachable will wait for
// the target URL's port to accept TCP connections before giving up. The
// cold-start path (agent injection + SSH session + listener bind) can take
// tens of seconds; the reuse path skips the probe entirely, so this only
// costs latency on the first launch.
const (
	browserProbeBudget   = 30 * time.Second
	browserProbeInterval = 200 * time.Millisecond
	browserProbeDial     = 300 * time.Millisecond
)

// KillBrowserTunnel terminates the detached browser tunnel for a workspace
// (if any) and removes its state file. Missing state and dead/foreign PIDs
// are tolerated silently; lock contention is logged at Warn level. Safe to
// call from any workspace teardown or recreate path.
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

// prepareInheritedListenersOrFallback wraps prepareInheritedListeners with
// the error-handling policy: EADDRINUSE means the port got stolen between
// probe and bind (expected; log at Debug and fall back to the legacy racy
// path). Any other error (FD exhaustion, permission denied, transient
// syscall failure) is logged at Warn so the silent regression to the racy
// path is visible.
func prepareInheritedListenersOrFallback(extraPorts []string) inheritedListenerSetup {
	setup, err := prepareInheritedListeners(extraPorts)
	if err == nil {
		return setup
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		pkglog.Debugf("inherit-listener: port stolen between probe and bind, falling back: %v", err)
	} else {
		pkglog.Warnf(
			"inherit-listener preparation failed (falling back to legacy racy port-binding path): %v",
			err,
		)
	}
	return inheritedListenerSetup{Cleanup: func() {}}
}

// inheritedListenerSetup bundles the result of prepareInheritedListeners so
// the multi-value return stays manageable.
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

// startDetachedBrowserTunnel spawns `devsy internal helper browser-tunnel ...` as a
// detached background process so the CLI can return to the prompt while the
// tunnel remains running.
//
// If params.Client is a DaemonClient (pro mode) this falls back to running
// the tunnel inline because the daemon already runs out-of-process.
//
// If a tunnel is already running for this workspace, no new process is
// spawned; the existing tunnel is reused and its recorded TargetURL is
// returned (which may differ from tunnelParams.TargetURL when the caller
// allocated a new port that the existing helper isn't using).
func startDetachedBrowserTunnel(
	ctx context.Context,
	params IDEParams,
	tunnelParams tunnel.BrowserTunnelParams,
	inv browserIDEInvocation,
) (string, error) {
	label := inv.Label
	openBrowser := inv.OpenBrowser
	contextName := params.Client.Context()
	workspaceID := params.Client.Workspace()

	if _, ok := params.Client.(client.DaemonClient); ok {
		return runDaemonBrowserTunnel(ctx, daemonTunnelArgs{
			ContextName:  contextName,
			WorkspaceID:  workspaceID,
			TunnelParams: tunnelParams,
			Inv:          inv,
			OpenBrowser:  openBrowser,
		})
	}

	// Serialize concurrent attempts (e.g. parallel `devsy up`) to avoid
	// orphaning helper processes by racing on the state file.
	unlock, err := acquireTunnelLock(contextName, workspaceID)
	if err != nil {
		return "", fmt.Errorf(
			"acquire tunnel lock (refusing to spawn unlocked to avoid orphan helpers): %w",
			err,
		)
	}
	defer unlock()

	// Reuse an existing live tunnel if present; clear stale state otherwise.
	if reusedURL, ok := tryReuseExistingTunnel(ctx, contextName, workspaceID, inv); ok {
		return reusedURL, nil
	}

	pid, logLocation, err := spawnTunnelHelper(spawnHelperOpts{
		ContextName:  contextName,
		WorkspaceID:  workspaceID,
		TunnelParams: tunnelParams,
		Label:        label,
		OpenBrowser:  openBrowser,
	})
	if err != nil {
		return "", err
	}

	pkglog.Infof(
		"%s browser tunnel running in background (PID %d). Logs: %s. "+
			"Run 'devsy stop %s' to terminate.",
		label, pid, logLocation, workspaceID,
	)

	// The browser auto-open is owned by the helper (via --open-browser),
	// not by this process: the parent CLI exits in milliseconds and would
	// reap any goroutine started here before the probe could run.

	return tunnelParams.TargetURL, nil
}

// spawnHelperOpts groups the inputs spawnTunnelHelper needs. OpenBrowser
// sits here rather than on tunnel.BrowserTunnelParams because the
// runtime-loaded helper owns the browser-launch end-to-end; threading it
// through the shared params struct would imply other callers can request
// auto-open too, which they cannot.
type spawnHelperOpts struct {
	ContextName  string
	WorkspaceID  string
	TunnelParams tunnel.BrowserTunnelParams
	Label        string
	OpenBrowser  bool
}

// spawnTunnelHelper locates the current executable, builds the helper
// argument list, pre-binds host listeners (where supported), spawns the
// detached helper process, and records its PID in the tunnel state file.
// It returns the spawned helper PID and the log location that should be
// reported to the user.
func spawnTunnelHelper(opts spawnHelperOpts) (int, string, error) {
	contextName := opts.ContextName
	workspaceID := opts.WorkspaceID
	tunnelParams := opts.TunnelParams
	label := opts.Label

	execPath, err := os.Executable()
	if err != nil {
		return 0, "", fmt.Errorf("locate executable: %w", err)
	}

	args := buildHelperArgs(contextName, workspaceID, tunnelParams, opts.OpenBrowser)

	// Pre-bind the host listeners in the parent so the chosen ports are
	// truly reserved before the helper forks. On Windows this is a no-op.
	setup := prepareInheritedListenersOrFallback(tunnelParams.ExtraPorts)
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
	openBrowser bool,
) []string {
	args := []string{
		"internal", "helper", "browser-tunnel",
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
	if openBrowser {
		args = append(args, "--open-browser")
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
// workspace, if any. On reuse it returns the recorded TargetURL so the
// caller can surface the live URL (not its own freshly-computed one).
// If state exists but the recorded PID is dead, the stale state file is
// removed and ("", false) is returned so the caller can proceed with a
// fresh spawn.
func tryReuseExistingTunnel(
	ctx context.Context,
	contextName, workspaceID string,
	inv browserIDEInvocation,
) (string, bool) {
	existing, _ := ReadTunnelState(contextName, workspaceID)
	if existing == nil {
		return "", false
	}
	if helperMatchesState(existing) {
		pkglog.Infof(
			"%s browser tunnel already running (PID %d). Reusing existing tunnel at %s",
			inv.Label, existing.PID, existing.TargetURL,
		)
		if inv.OpenBrowser {
			go openBrowserAsync(ctx, existing.TargetURL)
		}
		return existing.TargetURL, true
	}
	if statePath, err := TunnelStateFilePath(contextName, workspaceID); err == nil {
		_ = os.Remove(statePath)
	}
	return "", false
}

// daemonTunnelArgs bundles the parameters for runDaemonBrowserTunnel to keep
// its signature small.
type daemonTunnelArgs struct {
	ContextName  string
	WorkspaceID  string
	TunnelParams tunnel.BrowserTunnelParams
	Inv          browserIDEInvocation
	OpenBrowser  bool
}

// runDaemonBrowserTunnel handles the daemon-client path: reuse an existing
// helper if one is live, otherwise start the in-process tunnel and open the
// browser only once the target URL becomes reachable.
func runDaemonBrowserTunnel(ctx context.Context, a daemonTunnelArgs) (string, error) {
	unlock, err := acquireTunnelLock(a.ContextName, a.WorkspaceID)
	if err != nil {
		return "", fmt.Errorf(
			"acquire tunnel lock (refusing to start unlocked to avoid orphan helpers): %w",
			err,
		)
	}
	defer unlock()

	if reusedURL, ok := tryReuseExistingTunnel(ctx, a.ContextName, a.WorkspaceID, a.Inv); ok {
		return reusedURL, nil
	}

	// Derive a cancellable context for the probe goroutine. When
	// StartBrowserTunnel returns (success or failure) the deferred
	// cancelProbe stops the probe so it does not keep polling a port that
	// will never come up and emit a misleading "didn't come up" warning
	// alongside the original error.
	probeCtx, cancelProbe := context.WithCancel(ctx)
	defer cancelProbe()

	if a.OpenBrowser {
		go openBrowserWhenReachable(probeCtx, a.TunnelParams.TargetURL)
	}
	return a.TunnelParams.TargetURL, tunnel.StartBrowserTunnel(ctx, a.TunnelParams)
}

// OpenBrowserWhenReachable polls the target URL's TCP port until it accepts
// connections, then opens the browser. Exits silently when ctx is cancelled
// (the caller already gave up — warning would duplicate the underlying
// error); emits a "didn't come up" warning only when the budget expires.
//
// Exported so the long-lived browser-tunnel helper can own the probe-then-
// open sequence rather than the short-lived parent CLI.
func OpenBrowserWhenReachable(ctx context.Context, url string) {
	openBrowserWhenReachable(ctx, url)
}

func openBrowserWhenReachable(ctx context.Context, url string) {
	hostPort, err := hostPortFromURL(url)
	if err != nil {
		pkglog.Warnf("could not parse browser-tunnel URL %q: %v; skipping auto-open", url, err)
		return
	}
	if err := probeTCPReachable(ctx, hostPort, browserProbeBudget); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		pkglog.Warnf(
			"browser-tunnel URL %s never became reachable within %s; "+
				"skipping browser auto-open (open it manually once the tunnel is up)",
			url, browserProbeBudget,
		)
		return
	}
	openBrowserAsync(ctx, url)
}

// probeTCPReachable polls hostPort until a TCP connection succeeds or ctx /
// budget is exhausted. It returns nil on the first successful dial,
// context.DeadlineExceeded when the budget elapses with no listener, or
// context.Canceled when the caller's ctx is cancelled first.
//
// Each dial uses its own short timeout so a hung host can't burn the whole
// budget on one attempt.
func probeTCPReachable(
	ctx context.Context,
	hostPort string,
	budget time.Duration,
) error {
	return wait.PollUntilContextTimeout(
		ctx,
		browserProbeInterval,
		budget,
		true, // try once at t=0 before the first interval
		func(dialCtx context.Context) (bool, error) {
			d := net.Dialer{Timeout: browserProbeDial}
			conn, err := d.DialContext(dialCtx, "tcp", hostPort)
			if err != nil {
				return false, nil // keep polling until budget/ctx
			}
			_ = conn.Close()
			return true, nil
		},
	)
}

// hostPortFromURL extracts a "host:port" suitable for net.DialTimeout from
// a URL string. When the URL has no explicit port the scheme's default
// (80 for http/ws, 443 for https/wss) is used.
//
// Uses u.Hostname() / u.Port() rather than u.Host so IPv6 literals are
// unbracketed for net.JoinHostPort, which adds brackets itself when
// needed. Passing the bracketed u.Host directly produces malformed
// "[[::1]]:80" double-bracketing on the no-port path.
func hostPortFromURL(rawURL string) (string, error) {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("url %q has no host", rawURL)
	}
	port := u.Port()
	if port == "" {
		port, err = defaultPortForScheme(u.Scheme)
		if err != nil {
			return "", err
		}
	}
	return net.JoinHostPort(host, port), nil
}

// defaultPortForScheme returns the well-known port for http/https schemes.
func defaultPortForScheme(scheme string) (string, error) {
	switch scheme {
	case "http", "ws":
		return "80", nil
	case "https", "wss":
		return "443", nil
	default:
		return "", fmt.Errorf("unsupported scheme %q (no default port)", scheme)
	}
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
