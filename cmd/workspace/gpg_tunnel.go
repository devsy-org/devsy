package workspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/gpg"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

// gpgForwardFailedOSC is a private-use OSC identifier the desktop app's
// terminal (xterm.js) listens for; see Terminal.svelte's registerOscHandler.
const gpgForwardFailedOSC = 9977

// gpgForwardFailedReasonMaxLen bounds the OSC payload, since the desktop
// toast renders reason verbatim and it can originate from a remote error.
const gpgForwardFailedReasonMaxLen = 256

const (
	gpgForwardDiagnosticFileEnv = "DEVSY_GPG_FORWARD_DIAGNOSTIC_FILE"
	gpgForwardSessionIDEnv      = "DEVSY_GPG_FORWARD_SESSION_ID"
)

func writeGPGForwardFailedOSC(w io.Writer, reason string) {
	runes := []rune(reason)
	if len(runes) > gpgForwardFailedReasonMaxLen {
		runes = runes[:gpgForwardFailedReasonMaxLen]
	}
	clean := strings.Map(func(r rune) rune {
		// Strip C0/C1 controls (including BEL, ESC, ST) and ';', the OSC
		// parameter separator, so reason can't corrupt or extend the sequence.
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == ';' {
			return -1
		}
		return r
	}, string(runes))
	_, _ = fmt.Fprintf(w, "\x1b]%d;%s\a", gpgForwardFailedOSC, clean)
}

// gpgTunnelHealthCheckInterval is how often gpgTunnel.run re-checks the GPG
// tunnel once the session is up, so a terminal whose forward died with
// another (owning) terminal's disconnect can re-establish it itself.
const gpgTunnelHealthCheckInterval = 30 * time.Second

const gpgForwardStopTimeout = 2 * time.Second

// gpgTunnel owns the lifecycle of GPG-agent forwarding for one SSH session:
// deciding whether it's requested, owning the reverse-forward lifecycle,
// running the remote setup-gpg step, and periodically checking the tunnel.
type gpgTunnel struct {
	cmd     *SSHCmd
	enabled bool

	startForward func(context.Context, *ssh.Client, []string) (*managedReverseForward, error)

	// forward is nil unless this session currently owns a live forwarding loop.
	// Its completion is reconciled before setup so an exited loop can be rebound.
	forward *managedReverseForward

	// User mappings outlive the managed GPG loop and must only be bound once.
	userReverseForwardsStarted bool

	// failureReported prevents a repeated OSC 9977 notification while the
	// tunnel stays down across health-check ticks.
	failureReported bool

	// readySignaled prevents signaling gpg forward readiness more than once.
	readySignaled bool

	// readyFile retains the *os.File wrapping the readiness fd across failed
	// write attempts, preventing premature finalization/closure of the fd.
	readyFile *os.File
}

// newGPGTunnel reports whether GPG-agent forwarding was requested via flag
// or context option, and returns a tunnel that no-ops everywhere if not.
func newGPGTunnel(cmd *SSHCmd, devsyConfig *config.Config) *gpgTunnel {
	return &gpgTunnel{
		cmd: cmd,
		enabled: cmd.GPGAgentForwarding ||
			devsyConfig.ContextOptionBool(config.ContextOptionGPGAgentForwarding),
	}
}

// run watches the tunnel for the life of ctx, (re-)establishing it whenever
// it's found down. Call this in a goroutine tied to a context that's
// cancelled as soon as the owning SSH session ends (see
// runGPGTunnelInBackground). The first ensure runs synchronously before this
// goroutine starts (see runGPGTunnelInBackground), so this loop only needs
// to handle the periodic re-checks.
func (t *gpgTunnel) run(ctx context.Context, sshClient *ssh.Client) {
	if !t.enabled {
		return
	}

	ticker := time.NewTicker(gpgTunnelHealthCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if t.ensure(ctx, sshClient) {
				t.signalReadyOnce()
			}
		}
	}
}

// ensure checks whether the GPG tunnel is currently live and, if not,
// (re-)establishes it. It reports whether the tunnel is live when it
// returns.
func (t *gpgTunnel) ensure(ctx context.Context, sshClient *ssh.Client) bool {
	if gpg.IsGpgTunnelRunning(ctx, t.cmd.User, sshClient) {
		log.Debugf("gpg tunnel is running, skipping setup")
		t.failureReported = false
		return true
	}
	err := t.setup(ctx, sshClient)
	if err == nil {
		t.failureReported = false
		return true
	}
	if gpg.IsGpgTunnelRunning(ctx, t.cmd.User, sshClient) {
		log.Debugf(
			"gpg tunnel setup failed but tunnel is live (won by a concurrent terminal): %v",
			err,
		)
		t.failureReported = false
		return true
	}
	if ctx.Err() != nil {
		log.Debugf("gpg tunnel setup aborted by context cancellation: %v", err)
		return false
	}
	if t.failureReported {
		return false
	}
	log.Warnf("gpg agent forwarding failed (continuing without it): %v", err)
	t.failureReported = true
	reason := gpgForwardFailureReason(err)
	if writeGPGForwardDiagnostic(err) {
		reason += ": details saved in workspace logs"
	}
	// Emit OSC code for UI to detect.
	writeGPGForwardFailedOSC(os.Stderr, reason)
	return false
}

type gpgForwardDiagnostic struct {
	Timestamp string `json:"timestamp"`
	Component string `json:"component"`
	Code      string `json:"code"`
	SessionID string `json:"sessionId,omitempty"`
	Message   string `json:"message"`
}

func writeGPGForwardDiagnostic(err error) bool {
	path, pathErr := desktopGPGDiagnosticPath(os.Getenv(gpgForwardDiagnosticFileEnv))
	if pathErr != nil {
		log.Debugf("resolve gpg agent forwarding diagnostic path: %v", pathErr)
		return false
	}

	record, marshalErr := marshalGPGForwardDiagnostic(err)
	if marshalErr != nil {
		log.Debugf("encode gpg agent forwarding diagnostic: %v", marshalErr)
		return false
	}
	// #nosec G304 G703 -- The path is validated to stay within Desktop's workspace log directory.
	file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if openErr != nil {
		log.Debugf("open gpg agent forwarding diagnostic: %v", openErr)
		return false
	}
	if n, writeErr := file.Write(append(record, '\n')); writeErr != nil || n != len(record)+1 {
		if writeErr == nil {
			writeErr = io.ErrShortWrite
		}
		log.Debugf("write gpg agent forwarding diagnostic: %v", writeErr)
		_ = file.Close()
		return false
	}
	if closeErr := file.Close(); closeErr != nil {
		log.Debugf("close gpg agent forwarding diagnostic: %v", closeErr)
		return false
	}
	return true
}

func marshalGPGForwardDiagnostic(err error) ([]byte, error) {
	message := secrets.NewEnvironmentRedactor(os.Environ()).Redact(err.Error())
	if runes := []rune(message); len(runes) > 2048 {
		message = string(runes[:2048])
	}
	return json.Marshal(gpgForwardDiagnostic{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Component: "gpg-forwarding",
		Code:      gpgForwardFailureCode(err),
		SessionID: os.Getenv(gpgForwardSessionIDEnv),
		Message:   message,
	})
}

func desktopGPGDiagnosticPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("diagnostic file path is not configured")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	root, err := filepath.Abs(filepath.Join(home, ".devsy", "desktop", "logs", "workspaces"))
	if err != nil {
		return "", fmt.Errorf("resolve Desktop log directory: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve diagnostic file path: %w", err)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("diagnostic path is outside Desktop workspace logs")
	}
	return path, nil
}

func gpgForwardFailureCode(err error) string {
	switch {
	case strings.Contains(err.Error(), "start gpg-agent reverse forward"):
		return "reverse_forward_failed"
	case strings.Contains(err.Error(), "detect gpg-agent socket path"):
		return "host_agent_socket_unavailable"
	case strings.Contains(err.Error(), "export local ownertrust from GPG"):
		return "host_gpg_configuration_unavailable"
	default:
		return "setup_failed"
	}
}

func gpgForwardFailureReason(err error) string {
	switch {
	case strings.Contains(err.Error(), "start gpg-agent reverse forward"):
		return "GPG reverse forwarding failed"
	case strings.Contains(err.Error(), "detect gpg-agent socket path"):
		return "Host GPG agent socket unavailable"
	case strings.Contains(err.Error(), "export local ownertrust from GPG"):
		return "Unable to read host GPG configuration"
	default:
		return "GPG agent setup failed"
	}
}

// setup runs the remote setup-gpg command, which imports the host's owner trust
// and signing key into the container's gpg-agent. It also ensures this session
// owns a live reverse-forward listener before configuring the remote socket.
func (t *gpgTunnel) setup(ctx context.Context, containerClient *ssh.Client) error {
	log.Debugf("detecting gpg-agent socket path on host")
	// this socket gets forwarded to the remote and symlinked in multiple paths
	gpgExtraSocketPath, err := gpg.DetectAgentSocketPath()
	if err != nil {
		return fmt.Errorf("detect gpg-agent socket path: %w", err)
	}
	log.Debugf("detected gpg-agent socket path %s", gpgExtraSocketPath)

	command, err := t.buildSetupCommand(ctx)
	if err != nil {
		return err
	}

	if err := t.ensureForwardBound(ctx, containerClient, gpgExtraSocketPath); err != nil {
		return err
	}

	writer, writerDone := log.PipeJSONStream()
	defer func() {
		_ = writer.Close()
		<-writerDone
	}()
	if err := devssh.Run(ctx, devssh.RunOptions{
		Client:  containerClient,
		Command: command,
		Stdout:  writer,
		Stderr:  writer,
	}); err != nil {
		return fmt.Errorf("run gpg agent setup command: %w", err)
	}

	return nil
}

// buildSetupCommand assembles the remote `setup-gpg` invocation, exporting
// the host's owner trust and signing key into its arguments.
func (t *gpgTunnel) buildSetupCommand(ctx context.Context) (string, error) {
	cmd := t.cmd

	log.Debugf("exporting gpg owner trust from host")
	ownerTrustExport, err := gpg.GetHostOwnerTrust()
	if err != nil {
		return "", fmt.Errorf("export local ownertrust from GPG: %w", err)
	}
	ownerTrustArgument := base64.StdEncoding.EncodeToString(ownerTrustExport)

	gitKey := gpg.SigningKey(ctx)

	forwardAgent := []string{
		config.ContainerDevsyHelperLocation,
		"internal",
		"agent",
		"workspace",
		"setup-gpg",
		names.Flag(names.OwnerTrust),
		ownerTrustArgument,
		names.Flag(names.SocketPath),
		gpg.ContainerSocketPath,
	}
	if log.DebugEnabled() {
		forwardAgent = append(forwardAgent, names.Flag(names.Debug))
	}
	if gitKey != "" {
		forwardAgent = append(forwardAgent, names.Flag(names.GitKey), gitKey)
	}

	command := shellescape.QuoteCommand(forwardAgent)
	if cmd.User != "" && cmd.User != "root" {
		command = shellescape.QuoteCommand([]string{"su", "-c", command, cmd.User})
	}
	return command, nil
}

// ensureForwardBound keeps the GPG reverse-forward loop alive and starts a
// replacement after the previous loop exits.
func (t *gpgTunnel) ensureForwardBound(
	ctx context.Context,
	containerClient *ssh.Client,
	gpgExtraSocketPath string,
) error {
	if t.reconcileForward() {
		return nil
	}

	log.Debugf(
		"start reverse forward of gpg-agent socket %s, keeping connection open",
		gpgExtraSocketPath,
	)
	reverseForwardPorts := []string{gpg.ContainerSocketPath + ":" + gpgExtraSocketPath}
	if !t.userReverseForwardsStarted {
		reverseForwardPorts = append(reverseForwardPorts, t.cmd.ReverseForwardPorts...)
	}
	startForward := t.startForward
	if startForward == nil {
		startForward = t.cmd.startReverseForwardsAndWait
	}
	forward, err := startForward(ctx, containerClient, reverseForwardPorts)
	if err != nil {
		return fmt.Errorf("start gpg-agent reverse forward: %w", err)
	}
	t.forward = forward
	t.userReverseForwardsStarted = true
	return nil
}

// reconcileForward reports whether this tunnel still owns an active forward.
func (t *gpgTunnel) reconcileForward() bool {
	if t.forward == nil {
		return false
	}
	select {
	case err, ok := <-t.forward.done:
		t.forward = nil
		if ok && err != nil {
			log.Debugf("gpg agent reverse forward exited: %v", err)
		}
		return false
	default:
		return true
	}
}

// signalReadyOnce signals gpg forward readiness at most once per gpgTunnel:
// once the write actually succeeds, so a later health-check retry that also
// succeeds does not write the readiness byte again.
func (t *gpgTunnel) signalReadyOnce() {
	if t.readySignaled {
		return
	}
	t.readySignaled = t.signalGPGForwardReady()
}

func (t *gpgTunnel) signalGPGForwardReady() bool {
	fdStr := os.Getenv(gpg.EnvForwardReadyFD)
	if fdStr == "" {
		return false
	}
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		log.Debugf("invalid %s=%q: %v", gpg.EnvForwardReadyFD, fdStr, err)
		return false
	}
	if t.readyFile == nil {
		t.readyFile = os.NewFile(uintptr(fd), "gpg-forward-ready")
		if t.readyFile == nil {
			log.Debugf("invalid %s=%q: file descriptor is not open", gpg.EnvForwardReadyFD, fdStr)
			return false
		}
	}
	if _, err := t.readyFile.Write([]byte{1}); err != nil {
		log.Debugf("signal gpg forward ready: %v", err)
		// the fd may still be usable for a later retry
		return false
	}
	_ = t.readyFile.Close()
	t.readyFile = nil
	return true
}

// runGPGTunnelInBackground runs the tunnel's first setup synchronously, so
// the SSH command that follows does not race a still-forwarding gpg-agent,
// then starts t.run's periodic health-check loop in a goroutine tied to a
// context derived from ctx. It returns a wait func that cancels that context
// and blocks until the goroutine exits. Callers defer the wait func
// immediately after starting the tunnel, so a session that returns while the
// tunnel's health-check loop is mid-tick does not block on the session's own
// ctx.
func runGPGTunnelInBackground(
	ctx context.Context,
	t *gpgTunnel,
	sshClient *ssh.Client,
) (wait func()) {
	tunnelCtx, cancel := context.WithCancel(ctx)
	if t.enabled && t.ensure(tunnelCtx, sshClient) {
		t.signalReadyOnce()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		t.run(tunnelCtx, sshClient)
	}()
	return func() {
		cancel()
		<-done
		t.stopForward()
	}
}

func (t *gpgTunnel) stopForward() {
	if t.forward == nil {
		return
	}
	t.forward.cancel()
	select {
	case <-t.forward.done:
	case <-time.After(gpgForwardStopTimeout):
		log.Debugf("timed out waiting for gpg agent reverse forward to stop")
	}
	t.forward = nil
}
