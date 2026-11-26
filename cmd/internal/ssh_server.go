package cmdinternal

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/sharedfile"
	sshserver "github.com/devsy-org/devsy/pkg/ssh/server"
	"github.com/devsy-org/devsy/pkg/ssh/server/port"
	"github.com/devsy-org/devsy/pkg/stdio"
	"github.com/devsy-org/devsy/pkg/token"
	"github.com/devsy-org/ssh"
	"github.com/spf13/cobra"
)

const activityFileMode = 0o666

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
	f.StringVar(&cmd.address, names.Address,
		fmt.Sprintf("0.0.0.0:%d", sshserver.DefaultPort),
		"Address to listen to")
	f.BoolVar(&cmd.stdio, names.Stdio, false,
		"Listen on stdin/stdout instead of an address")
	f.BoolVar(&cmd.trackActivity, names.TrackActivity, false,
		"Record activity while connected (only with --stdio)")
	f.StringVar(&cmd.reuseSSHAuthSock, names.ReuseSSHAuthSock, "",
		"If set, reuse a pre-existing SSH_AUTH_SOCK in the workspace under /tmp")
	_ = f.MarkHidden(names.ReuseSSHAuthSock)
	f.StringVar(&cmd.token, names.Token, "", "Base64 encoded token to use")
	f.StringVar(&cmd.workdir, names.Workdir, "",
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
	log.Debugf("starting ssh server: stdio=%v", cmd.stdio)
	sshserver.SweepStaleAgentSockets()

	server, err := sshserver.NewServer(cmd.address, sshserver.ServerOptions{
		HostKey:   hostKey,
		Keys:      keys,
		Workdir:   cmd.workdir,
		ReuseSock: cmd.reuseSSHAuthSock,
	})
	if err != nil {
		return fmt.Errorf("create ssh server: %w", err)
	}
	log.Debug("ssh server initialized")

	if cmd.stdio {
		return cmd.serveStdio(ctx, server)
	}
	return cmd.serveListener(ctx, server)
}

func (cmd *sshServerCmd) serveStdio(ctx context.Context, server sshserver.Server) error {
	if cmd.trackActivity {
		workCtx, stop, err := agentd.StartActivityWriter(ctx, agentd.DefaultActivityInterval)
		if err != nil {
			return fmt.Errorf("register SSH activity: %w", err)
		}
		ctx = workCtx
		defer stop()
	}
	go shutdownOnCancel(ctx, server) // #nosec G118 -- see shutdownOnCancel.
	lis := stdio.NewStdioListener(os.Stdin, os.Stdout)
	log.Debug("serving ssh on stdio")
	err := ignoreServerClosed(server.Serve(lis))
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
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

// ignoreServerClosed turns expected listener shutdown errors into a clean
// return so cobra doesn't surface them as a failure.
func ignoreServerClosed(err error) error {
	if err == nil || errors.Is(err, ssh.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
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

func ensureActivityFile(path string) error {
	return sharedfile.EnsureMode(path, activityFileMode)
}

// touchActivityFile records discrete legacy activity (for example fleet-server).
func touchActivityFile(path string) {
	if err := ensureActivityFile(path); err != nil {
		log.Errorf("touch activity file: ensure file: %v", err)
		return
	}
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		log.Errorf("touch activity file: %v", err)
	}
}
