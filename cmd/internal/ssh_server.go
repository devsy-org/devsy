package cmdinternal

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	sshserver "github.com/devsy-org/devsy/pkg/ssh/server"
	"github.com/devsy-org/devsy/pkg/ssh/server/port"
	"github.com/devsy-org/devsy/pkg/stdio"
	"github.com/devsy-org/devsy/pkg/token"
	"github.com/devsy-org/ssh"
	"github.com/spf13/cobra"
)

const (
	activityHeartbeatInterval = 10 * time.Second
	activityFileMode          = 0o666
)

// shutdownTimeout bounds the grace period for in-flight SSH connections to
// drain on context cancel before the listener is force-closed.
const shutdownTimeout = 5 * time.Second

// sshServerCmd holds the ssh server cmd flags.
type sshServerCmd struct {
	*flags.GlobalFlags

	token            string
	address          string
	stdio            bool
	trackActivity    bool
	reuseSSHAuthSock string
	workdir          string
}

// NewSSHServerCmd creates a new ssh command.
func NewSSHServerCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &sshServerCmd{GlobalFlags: globalFlags}
	sshCmd := &cobra.Command{
		Use:   "ssh-server",
		Short: "Starts a new SSH server",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.run(cobraCmd.Context())
		},
	}

	f := sshCmd.Flags()
	f.StringVar(&cmd.address, "address",
		fmt.Sprintf("0.0.0.0:%d", sshserver.DefaultPort),
		"Address to listen to")
	f.BoolVar(&cmd.stdio, "stdio", false,
		"Listen on stdin/stdout instead of an address")
	f.BoolVar(&cmd.trackActivity, "track-activity", false,
		"Touch the activity file every "+activityHeartbeatInterval.String()+" (only with --stdio)")
	f.StringVar(&cmd.reuseSSHAuthSock, "reuse-ssh-auth-sock", "",
		"If set, reuse a pre-existing SSH_AUTH_SOCK in the workspace under /tmp")
	_ = f.MarkHidden("reuse-ssh-auth-sock")
	f.StringVar(&cmd.token, "token", "", "Base64 encoded token to use")
	f.StringVar(&cmd.workdir, "workdir", "",
		"Directory where commands will run on the host")
	return sshCmd
}

func (cmd *sshServerCmd) run(ctx context.Context) error {
	if cmd.trackActivity && !cmd.stdio {
		return errors.New("--track-activity requires --stdio")
	}

	keys, hostKey, err := parseSSHToken(cmd.token)
	if err != nil {
		return err
	}

	// Sweep stale per-connection agent socket directories left behind by
	// predecessors killed by docker exec / proxy-chain teardown before
	// internal SSH cleanup could run. Liveness is decided via a per-directory
	// flock the owning process holds for its lifetime; the kernel releases
	// the flock on any process exit (including SIGKILL).
	sshserver.SweepStaleAgentSockets()

	server, err := sshserver.NewServer(
		cmd.address, hostKey, keys, cmd.workdir, cmd.reuseSSHAuthSock,
	)
	if err != nil {
		return fmt.Errorf("create ssh server: %w", err)
	}

	if cmd.stdio {
		return cmd.serveStdio(ctx, server)
	}
	return cmd.serveListener(ctx, server)
}

func (cmd *sshServerCmd) serveStdio(ctx context.Context, server sshserver.Server) error {
	if cmd.trackActivity {
		go runActivityHeartbeat(ctx, config.ContainerActivityFile)
	}
	go shutdownOnCancel(ctx, server) // #nosec G118 -- see shutdownOnCancel.
	lis := stdio.NewStdioListener(os.Stdin, os.Stdout, true)
	return ignoreServerClosed(server.Serve(lis))
}

func (cmd *sshServerCmd) serveListener(ctx context.Context, server sshserver.Server) error {
	available, err := port.IsAvailable(cmd.address)
	if err != nil {
		return fmt.Errorf("check port %s: %w", cmd.address, err)
	}
	if !available {
		return fmt.Errorf("address %s already in use", cmd.address)
	}
	go shutdownOnCancel(ctx, server) // #nosec G118 -- see shutdownOnCancel.
	return ignoreServerClosed(server.ListenAndServe())
}

// shutdownOnCancel drains active SSH connections when ctx is canceled,
// bounded by shutdownTimeout. The shutdown context derives from
// context.Background by design: ctx is already canceled at this point, so
// reusing it would make Shutdown's grace period instantly expire.
func shutdownOnCancel(ctx context.Context, server sshserver.Server) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Errorf("ssh server shutdown: %v", err)
	}
}

// ignoreServerClosed turns the expected post-Shutdown error into a clean
// return so cobra doesn't surface "ssh: Server closed" as a failure.
func ignoreServerClosed(err error) error {
	if err == nil || errors.Is(err, ssh.ErrServerClosed) {
		return nil
	}
	return err
}

// parseSSHToken decodes the optional base64-encoded token blob. An empty
// string is valid and yields zero values: callers may run without a token.
func parseSSHToken(encoded string) ([]ssh.PublicKey, []byte, error) {
	if encoded == "" {
		return nil, nil, nil
	}

	t, err := token.ParseToken(encoded)
	if err != nil {
		return nil, nil, fmt.Errorf("parse token: %w", err)
	}

	keys, err := decodeAuthorizedKeys(t.AuthorizedKeys)
	if err != nil {
		return nil, nil, err
	}

	hostKey, err := decodeBase64Bytes(t.HostKey, "host key")
	if err != nil {
		return nil, nil, err
	}

	return keys, hostKey, nil
}

func decodeAuthorizedKeys(encoded string) ([]ssh.PublicKey, error) {
	if encoded == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode authorized keys: %w", err)
	}
	var keys []ssh.PublicKey
	for len(raw) > 0 {
		key, _, _, rest, err := ssh.ParseAuthorizedKey(raw)
		if err != nil {
			return nil, fmt.Errorf("parse authorized key: %w", err)
		}
		keys = append(keys, key)
		raw = rest
	}
	return keys, nil
}

func decodeBase64Bytes(encoded, label string) ([]byte, error) {
	if encoded == "" {
		return nil, nil
	}
	out, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	return out, nil
}

// runActivityHeartbeat periodically updates the activity file's mtime so
// outside watchers can detect liveness. Exits when ctx is canceled. Logs
// only on success/failure transitions so a permanently broken file does
// not spam the log.
func runActivityHeartbeat(ctx context.Context, path string) {
	if err := ensureActivityFile(path); err != nil {
		log.Errorf("activity heartbeat: ensure file: %v", err)
		return
	}

	t := time.NewTicker(activityHeartbeatInterval)
	defer t.Stop()

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			err := os.Chtimes(path, now, now)
			if (err == nil) != (lastErr == nil) {
				if err != nil {
					log.Errorf("activity heartbeat: %v", err)
				} else {
					log.Infof("activity heartbeat recovered")
				}
			}
			lastErr = err
		}
	}
}

func ensureActivityFile(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat: %w", err)
	}
	if err := os.WriteFile(
		path,
		nil,
		activityFileMode,
	); err != nil { // #nosec G306 -- intentionally world-writable; multiple users update activity
		return fmt.Errorf("create: %w", err)
	}
	if err := os.Chmod(path, activityFileMode); err != nil { // #nosec G302 -- ditto
		return fmt.Errorf("chmod: %w", err)
	}
	return nil
}
