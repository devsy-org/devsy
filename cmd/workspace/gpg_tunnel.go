package workspace

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/gpg"
	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

// gpgForwardFailedOSC is a private-use OSC identifier the desktop app's
// terminal (xterm.js) listens for to surface a non-fatal GPG-forwarding
// failure as a toast; see Terminal.svelte's registerOscHandler.
const gpgForwardFailedOSC = 9977

// gpgForwardFailedReasonMaxLen bounds the OSC payload, since the desktop
// toast renders reason verbatim and it can originate from a remote error.
const gpgForwardFailedReasonMaxLen = 256

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

// gpgTunnel owns the lifecycle of GPG-agent forwarding for one SSH session:
// deciding whether it's requested, binding the reverse-listen socket at most
// once, running the remote setup-gpg step, and periodically checking the
// tunnel is still alive for as long as the session runs.
type gpgTunnel struct {
	cmd     *SSHCmd
	enabled bool

	// forwardBound guards against re-binding the reverse-listen socket on a
	// health-check retry: the listener stays open for the session, and the
	// server rejects a second bind of the same path.
	forwardBound bool

	// failureReported prevents a repeated OSC 9977 notification while the
	// tunnel stays down across health-check ticks.
	failureReported bool
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
// runGPGTunnelInBackground).
func (t *gpgTunnel) run(ctx context.Context, sshClient *ssh.Client) {
	if !t.enabled {
		return
	}

	t.ensure(ctx, sshClient)

	ticker := time.NewTicker(gpgTunnelHealthCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.ensure(ctx, sshClient)
		}
	}
}

// ensure checks whether the GPG tunnel is currently live and, if not,
// (re-)establishes it. A setup failure is only reported if the tunnel is
// still down immediately after: a concurrent terminal may have won the bind
// race in the interim, which isn't a real failure. The OSC failure
// notification fires only on the healthy-to-failed transition (gated by
// failureReported), not on every health-check tick.
func (t *gpgTunnel) ensure(ctx context.Context, sshClient *ssh.Client) {
	if gpg.IsGpgTunnelRunning(ctx, t.cmd.User, sshClient) {
		log.Debugf("GPG tunnel is running, skipping setup")
		t.failureReported = false
		return
	}
	err := t.setup(ctx, sshClient)
	if err == nil {
		t.failureReported = false
		return
	}
	if gpg.IsGpgTunnelRunning(ctx, t.cmd.User, sshClient) {
		log.Debugf(
			"GPG tunnel setup failed but tunnel is live (won by a concurrent terminal): %v",
			err,
		)
		t.failureReported = false
		return
	}
	if ctx.Err() != nil {
		// ctx was cancelled (session ending); the failure above is an
		// artifact of that, not a real forwarding problem worth reporting.
		log.Debugf("GPG tunnel setup aborted by context cancellation: %v", err)
		return
	}
	log.Warnf("GPG agent forwarding failed (continuing without it): %v", err)
	if t.failureReported {
		return
	}
	t.failureReported = true
	// The desktop toast gets a fixed, concise reason rather than err.Error():
	// the underlying error can wrap remote SSH exit output, which isn't
	// something to surface verbatim in the UI. Full detail stays in the log
	// line above (visible with --debug).
	writeGPGForwardFailedOSC(os.Stderr, "check logs for details")
}

// setup forwards the local gpg-agent into the remote container by using
// cmd/internal/agentworkspace/setup_gpg.
func (t *gpgTunnel) setup(ctx context.Context, containerClient *ssh.Client) error {
	log.Debugf("detecting gpg-agent socket path on host")
	// Detect local agent extra socket, this will be forwarded to the remote and
	// symlinked in multiple paths
	gpgExtraSocketPath, err := gpg.DetectAgentSocketPath()
	if err != nil {
		return err
	}
	log.Debugf("[GPG] detected gpg-agent socket path %s", gpgExtraSocketPath)

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

	log.Debugf("[GPG] exporting gpg owner trust from host")
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

// ensureForwardBound binds the reverse-listen socket at most once per
// process (see forwardBound); setup's remote command still re-runs every
// time to repair remote-side agent state (stopped agent, stale keys), which
// doesn't require touching the reverse-forward at all.
func (t *gpgTunnel) ensureForwardBound(
	ctx context.Context,
	containerClient *ssh.Client,
	gpgExtraSocketPath string,
) error {
	if t.forwardBound {
		return nil
	}

	log.Debugf(
		"[GPG] start reverse forward of gpg-agent socket %s, keeping connection open",
		gpgExtraSocketPath,
	)
	reverseForwardPorts := append(
		[]string{gpg.ContainerSocketPath + ":" + gpgExtraSocketPath},
		t.cmd.ReverseForwardPorts...,
	)
	err := t.cmd.startReverseForwardsAndWait(ctx, containerClient, reverseForwardPorts)
	if err != nil {
		return fmt.Errorf("start gpg-agent reverse forward: %w", err)
	}
	t.forwardBound = true
	return nil
}

// runGPGTunnelInBackground starts t.run in a goroutine tied to a context
// derived from ctx, and returns a wait func that cancels that context and
// blocks until the goroutine exits. Callers defer the wait func immediately
// after starting the tunnel, so a session that returns while the tunnel's
// health-check loop is mid-tick doesn't block on the session's own (often
// much longer-lived) ctx.
func runGPGTunnelInBackground(
	ctx context.Context,
	t *gpgTunnel,
	sshClient *ssh.Client,
) (wait func()) {
	tunnelCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t.run(tunnelCtx, sshClient)
	}()
	return func() {
		cancel()
		<-done
	}
}
