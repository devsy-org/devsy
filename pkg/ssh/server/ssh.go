package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/devsy-org/ssh"
	gossh "golang.org/x/crypto/ssh"
)

// connAgentIntents maps an active SSH connection to its lazy agent intent.
// Cleanup is driven from ConnectionCompleteCallback, which runs as a defer
// in HandleConn BEFORE the stdio listener's Close()-on-EOF triggers os.Exit
// — that ordering is the only reliable way to guarantee the socket
// directory is removed before the process dies in stdio mode.
var connAgentIntents sync.Map // map[*gossh.ServerConn]*connAgentIntent

// ctxKey is a private type for ssh.Context keys.
type ctxKey int

const (
	ctxKeyConnAgent ctxKey = iota
)

// connAgentIntent is stored on the ssh.Context for every non-reuseSock
// connection. It carries the connection id plus a lazily-populated
// per-connection agent state. The actual listener + socket directory are
// only allocated on the first session that requests agent forwarding.
type connAgentIntent struct {
	connID string
	mu     sync.Mutex
	state  *connAgentState
	setErr error
	inited bool
}

// connAgentState holds the per-connection agent forwarding listener and
// its socket directory. ForwardAgentConnections is started lazily on the
// first session that requests agent forwarding, since it requires an
// ssh.Session to open the auth-agent channel back to the client.
type connAgentState struct {
	listener  net.Listener
	socketDir string
	socket    string
	once      sync.Once
}

// newConnAgentState constructs the per-connection agent state, allocating
// the unix-socket listener and its containing directory. The socket path
// always mirrors listener.Addr().String() so callers cannot drift the two.
func newConnAgentState(connID string) (*connAgentState, error) {
	l, socketDir, err := setupConnectionAgentListener(connID)
	if err != nil {
		return nil, err
	}
	return &connAgentState{
		listener:  l,
		socketDir: socketDir,
		socket:    l.Addr().String(),
	}, nil
}

// sockPath returns the unix socket path clients should set as $SSH_AUTH_SOCK.
func (c *connAgentState) sockPath() string {
	return c.socket
}

// startForwarding starts ForwardAgentConnections exactly once for the
// connection, bound to the first session that requests agent forwarding.
func (c *connAgentState) startForwarding(sess ssh.Session) {
	c.once.Do(func() {
		go ssh.ForwardAgentConnections(c.listener, sess)
	})
}

// close tears down the listener and removes the socket directory.
func (c *connAgentState) close() {
	if c == nil {
		return
	}
	if c.listener != nil {
		_ = c.listener.Close()
	}
	cleanupAgentSocketDir(c.socketDir)
}

const (
	DefaultPort     int = 8022
	DefaultUserPort int = 12023
)

type Server interface {
	Serve(listener net.Listener) error
	ListenAndServe() error
}

type server struct {
	currentUser string
	shell       []string
	workdir     string
	reuseSock   string
	sshServer   ssh.Server
}

func NewServer(
	addr string,
	hostKey []byte,
	keys []ssh.PublicKey,
	workdir string,
	reuseSock string,
) (Server, error) {
	sh, err := shell.GetShell("")
	if err != nil {
		return nil, err
	}

	currentUser, err := user.Current()
	if err != nil {
		return nil, err
	}

	forwardHandler := &ssh.ForwardedTCPHandler{}
	forwardedUnixHandler := &ssh.ForwardedUnixHandler{}
	server := &server{
		shell:       sh,
		workdir:     workdir,
		reuseSock:   reuseSock,
		currentUser: currentUser.Username,
		sshServer: ssh.Server{
			Addr: addr,
			// ClientAliveInterval + ClientAliveCountMax give the server a way
			// to notice a dead peer in stdio mode, where EOF on stdin can be
			// delayed indefinitely by the proxy chain. With these set, the
			// gossh connection eventually fails, HandleConn returns, and the
			// per-connection agent state can be torn down.
			ClientAliveInterval: 5 * time.Second,
			ClientAliveCountMax: 2,
			LocalPortForwardingCallback: func(ctx ssh.Context, dhost string, dport uint32) bool {
				log.Debugf("Accepted forward: %s:%d", dhost, dport)
				return true
			},
			ReversePortForwardingCallback: func(ctx ssh.Context, host string, port uint32) bool {
				log.Debugf("attempt to bind %s:%d - %s", host, port, "granted")
				return true
			},
			ReverseUnixForwardingCallback: func(ctx ssh.Context, socketPath string) bool {
				log.Debugf("attempt to bind socket %s", socketPath)

				_, err := os.Stat(socketPath)
				if err == nil {
					log.Debugf("%s already exists, removing", socketPath)

					_ = os.Remove(socketPath)
				}

				return true
			},
			ChannelHandlers: map[string]ssh.ChannelHandler{
				"direct-tcpip":                   ssh.DirectTCPIPHandler,
				"direct-streamlocal@openssh.com": ssh.DirectStreamLocalHandler,
				"session":                        ssh.DefaultSessionHandler,
			},
			RequestHandlers: map[string]ssh.RequestHandler{
				"tcpip-forward":                          forwardHandler.HandleSSHRequest,
				"streamlocal-forward@openssh.com":        forwardedUnixHandler.HandleSSHRequest,
				"cancel-streamlocal-forward@openssh.com": forwardedUnixHandler.HandleSSHRequest,
				"cancel-tcpip-forward":                   forwardHandler.HandleSSHRequest,
			},
			SubsystemHandlers: map[string]ssh.SubsystemHandler{
				"sftp": func(s ssh.Session) {
					sftpHandler(s, currentUser.Username)
				},
			},
		},
	}

	if len(keys) > 0 {
		server.sshServer.PublicKeyHandler = func(ctx ssh.Context, key ssh.PublicKey) bool {
			for _, k := range keys {
				if ssh.KeysEqual(k, key) {
					return true
				}
			}

			log.Debugf("Declined public key")
			return false
		}
	}

	if len(hostKey) > 0 {
		err = server.sshServer.SetOption(ssh.HostKeyPEM(hostKey))
		if err != nil {
			return nil, err
		}
	}

	server.sshServer.Handler = server.handler
	server.sshServer.ConnCallback = server.connCallback
	server.sshServer.ConnectionCompleteCallback = cleanupAgentOnConnComplete
	return server, nil
}

// cleanupAgentOnConnComplete tears down the per-connection agent state for
// a connection that just ended. Runs synchronously in HandleConn's defer
// chain, which is critical in stdio mode where the listener's Close hook
// calls os.Exit and ctx.Done()-driven goroutines never get to run.
func cleanupAgentOnConnComplete(sc *gossh.ServerConn, _ error) {
	log.Debugf("ssh ConnectionCompleteCallback fired (sc=%p)", sc)
	v, ok := connAgentIntents.LoadAndDelete(sc)
	if !ok {
		log.Debugf("ssh ConnectionCompleteCallback: no intent registered for sc=%p", sc)
		return
	}
	intent, ok := v.(*connAgentIntent)
	if !ok || intent == nil {
		return
	}
	intent.mu.Lock()
	state := intent.state
	intent.mu.Unlock()
	if state == nil {
		log.Debugf("ssh conn close: connID=%s (no listener allocated)", intent.connID)
		return
	}
	sock := state.sockPath()
	state.close()
	log.Debugf("ssh conn close: connID=%s agent_sock=%s cleaned up", intent.connID, sock)
}

// newConnID returns a short hex identifier unique to the connection.
// Uses crypto/rand; on the unlikely event of a rand.Read failure falls
// back to a sha256-of-time derivation so the connection still gets an ID.
func newConnID(remote string) string {
	var b [8]byte
	_, err := rand.Read(b[:])
	if err == nil {
		return hex.EncodeToString(b[:])
	}
	sum := sha256.Sum256([]byte(remote + strconv.FormatInt(time.Now().UnixNano(), 10)))
	return fmt.Sprintf("%x", sum)[:16]
}

// ensureConnAgentState lazily allocates the per-connection agent listener
// the first time an agent-forwarding session arrives. Subsequent calls
// return the same state (or the same error).
func (intent *connAgentIntent) ensureState() (*connAgentState, error) {
	intent.mu.Lock()
	defer intent.mu.Unlock()
	if intent.inited {
		return intent.state, intent.setErr
	}
	intent.inited = true
	state, err := newConnAgentState(intent.connID)
	intent.state = state
	intent.setErr = err
	return state, err
}

func (s *server) handler(sess ssh.Session) {
	var err error
	ptyReq, winCh, isPty := sess.Pty()
	cmd := s.getCommand(sess, isPty)

	if ssh.AgentRequested(sess) {
		if s.reuseSock != "" {
			// openvscode backhaul / explicit shared-socket mode: keep the
			// existing per-session listener behavior.
			l, tmpDir, err := setupAgentListener(s.reuseSock)
			if err != nil {
				exitWithError(sess, err)
				return
			}
			defer func() { _ = l.Close() }()
			defer func() { _ = os.RemoveAll(tmpDir) }()

			go ssh.ForwardAgentConnections(l, sess)

			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "SSH_AUTH_SOCK", l.Addr().String()))
		} else if intent, ok := sess.Context().Value(ctxKeyConnAgent).(*connAgentIntent); ok && intent != nil {
			// Common interactive case: lazily allocate the connection-scoped
			// listener on first request, then reuse it for every subsequent
			// session on the same connection. ForwardAgentConnections needs an
			// ssh.Session to open the auth-agent channel, so it is bound to
			// the first session that requests agent forwarding.
			state, sErr := intent.ensureState()
			if sErr != nil || state == nil {
				log.Errorf("ssh agent forwarding setup failed (connID=%s): %v", intent.connID, sErr)
				_, _ = fmt.Fprintf(
					sess.Stderr(),
					"warning: ssh agent forwarding unavailable: %v\n",
					sErr,
				)
				exitWithError(sess, sErr)
				return
			}
			// Register the intent against the underlying gossh.ServerConn so
			// cleanupAgentOnConnComplete can find and tear it down when the
			// connection ends.
			if sc, ok := sess.Context().Value(ssh.ContextKeyConn).(*gossh.ServerConn); ok &&
				sc != nil {
				connAgentIntents.LoadOrStore(sc, intent)
				log.Debugf("ssh intent registered: connID=%s sc=%p", intent.connID, sc)
			} else {
				log.Debugf(
					"ssh intent NOT registered (missing gossh.ServerConn): connID=%s",
					intent.connID,
				)
			}
			state.startForwarding(sess)
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "SSH_AUTH_SOCK", state.sockPath()))
		} else {
			log.Debugf("agent requested but no connection-scoped agent intent available")
		}
	}

	// start shell session
	if isPty {
		err = execPTY(ptyExecParams{
			sess:   sess,
			ptyReq: ptyReq,
			winCh:  winCh,
			cmd:    cmd,
		})
	} else {
		err = execNonPTY(sess, cmd)
	}

	// exit session
	exitWithError(sess, err)
}

func (s *server) getCommand(sess ssh.Session, isPty bool) *exec.Cmd {
	var cmd *exec.Cmd
	user := sess.User()
	if user == s.currentUser {
		user = ""
	}

	// has user set?
	if user != "" {
		args := []string{}

		// is pty?
		if isPty {
			args = append(args, "-")
		}

		// add user
		args = append(args, sess.User())

		// is there a command?
		if len(sess.RawCommand()) > 0 {
			args = append(args, "-c", sess.RawCommand())
		}

		cmd = exec.Command("su", args...)
	} else {
		args := []string{}
		args = append(args, s.shell[1:]...)
		if isPty {
			args = append(args, "-l")
		}

		if len(sess.RawCommand()) == 0 {
			cmd = exec.Command(s.shell[0], args...)
		} else {
			args = append(args, "-c", sess.RawCommand())
			cmd = exec.Command(s.shell[0], args...)
		}
	}

	cmd.Dir = findWorkdir(s.workdir, user)
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, sess.Environ()...)
	return cmd
}

func (s *server) Serve(listener net.Listener) error {
	return s.sshServer.Serve(listener)
}

func (s *server) ListenAndServe() error {
	log.Debugf("Start ssh server on %s", s.sshServer.Addr)
	return s.sshServer.ListenAndServe()
}

// connCallback is invoked once per inbound SSH connection. Outside the
// explicit reuseSock (openvscode backhaul) mode it stores a lightweight
// intent on the ssh.Context and schedules a teardown goroutine. The agent
// listener itself is allocated lazily on the first session that requests
// agent forwarding, so failed-auth probes never touch the filesystem.
func (s *server) connCallback(ctx ssh.Context, conn net.Conn) net.Conn {
	// Preserve the openvscode backhaul path: when a reuseSock is provided,
	// the per-session setupAgentListener(reuseSock) path is the intended
	// behavior. Skip setting up a per-connection listener here.
	if s.reuseSock != "" {
		return conn
	}

	intent := &connAgentIntent{connID: newConnID(conn.RemoteAddr().String())}
	ctx.SetValue(ctxKeyConnAgent, intent)

	log.Debugf("ssh conn open: connID=%s remote=%s", intent.connID, conn.RemoteAddr())
	return conn
}
