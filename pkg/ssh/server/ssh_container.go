package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	copypkg "github.com/devsy-org/devsy/pkg/copy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	shellpkg "github.com/devsy-org/devsy/pkg/shell"
	"github.com/devsy-org/ssh"
)

func NewContainerServer(addr string, workdir string) (Server, error) {
	forwardHandler := &ssh.ForwardedTCPHandler{}
	forwardedUnixHandler := &ssh.ForwardedUnixHandler{}
	server := &containerServer{
		workdir: workdir,
		sshServer: ssh.Server{
			Addr: addr,
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
					sftpHandler(s, "")
				},
			},
		},
	}

	server.sshServer.Handler = server.handler
	return server, nil
}

type containerServer struct {
	sshServer ssh.Server
	workdir   string
}

func (s *containerServer) Serve(listener net.Listener) error {
	return s.sshServer.Serve(listener)
}

func (s *containerServer) Shutdown(ctx context.Context) error {
	return s.sshServer.Shutdown(ctx)
}

func (s *containerServer) ListenAndServe() error {
	log.Debugf("Start ssh server on %s", s.sshServer.Addr)
	return s.sshServer.ListenAndServe()
}

func (s *containerServer) handler(sess ssh.Session) {
	var err error
	ptyReq, winCh, isPty := sess.Pty()
	cmd, err := s.getCommand(sess, isPty)
	if err != nil {
		exitWithError(sess, fmt.Errorf("get command: %w", err))
		return
	}

	if ssh.AgentRequested(sess) {
		l, tmpDir, err := setupAgentListener("")
		if err != nil {
			exitWithError(sess, err)
			return
		}
		defer func() { _ = l.Close() }()
		defer func() { _ = os.RemoveAll(tmpDir) }()

		err = chownListener(l.Addr().String(), sess.User())
		if err != nil {
			exitWithError(sess, fmt.Errorf("chown listener: %w", err))
			return
		}

		go ssh.ForwardAgentConnections(l, sess)

		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "SSH_AUTH_SOCK", l.Addr().String()))
	}

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

	exitWithError(sess, err)
}

func (s *containerServer) getCommand(sess ssh.Session, isPty bool) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	user := sess.User()

	// get login shell for user
	shell, err := shellpkg.GetShell(user)
	if err != nil {
		return cmd, fmt.Errorf("get shell for user %s: %w", user, err)
	}

	args := []string{}
	args = append(args, shell[1:]...)
	if isPty {
		args = append(args, "-l")
	}

	if len(sess.RawCommand()) == 0 {
		cmd = exec.Command(shell[0], args...)
	} else {
		args = append(args, "-c", sess.RawCommand())
		cmd = exec.Command(shell[0], args...)
	}

	err = config.PrepareCmdUser(cmd, user)
	if err != nil {
		return cmd, fmt.Errorf("prepare cmd env: %w", err)
	}
	cmd.Dir = findWorkdir(s.workdir, user)
	cmd.Env = append(cmd.Env, sess.Environ()...)
	secretEnv, err := readSessionSecretEnvironment(config.SecretsEnvDir)
	if err != nil {
		return cmd, fmt.Errorf("prepare session secret environment: %w", err)
	}
	cmd.Env = append(cmd.Env, secretEnv...)
	return cmd, nil
}

func readSessionSecretEnvironment(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	env := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || secrets.ValidateName(entry.Name()) != nil {
			continue
		}
		// #nosec G304 -- fixed secret directory and validated name.
		value, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read secret environment variable %s: %w", entry.Name(), err)
		}
		env = append(env, entry.Name()+"="+string(value))
	}
	return env, nil
}

func chownListener(listenerPath string, user string) error {
	err := copypkg.Chown(filepath.Dir(listenerPath), user)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = copypkg.Chown(listenerPath, user)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}
