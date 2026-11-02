package agentcontainer

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/devsy-org/devsy/cmd/flags"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	sshserver "github.com/devsy-org/devsy/pkg/ssh/server"
	"github.com/devsy-org/devsy/pkg/ssh/server/port"
	"github.com/spf13/cobra"
)

// SSHServerCmd holds the ssh server cmd flags.
type SSHServerCmd struct {
	*flags.GlobalFlags

	Address    string
	Workdir    string
	RemoteUser string

	// TrackActivity records connection activity for the inactivity timer.
	TrackActivity bool
}

// NewSSHServerCmd creates a new ssh command.
func NewSSHServerCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SSHServerCmd{
		GlobalFlags: flags,
	}
	sshCmd := &cobra.Command{
		Use:   "ssh-server",
		Short: "Starts the container SSH server",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}

	cliflags.Add(
		sshCmd,
		cliflags.String(
			&cmd.Address,
			names.Address,
			fmt.Sprintf("127.0.0.1:%d", sshserver.DefaultUserPort),
			"Address to listen to",
		),
		cliflags.String(
			&cmd.RemoteUser,
			names.RemoteUser,
			"",
			"The remote user for this workspace",
		),
		cliflags.String(
			&cmd.Workdir,
			names.Workdir,
			"",
			"Directory where commands will run on the host",
		),
		cliflags.Bool(
			&cmd.TrackActivity,
			names.TrackActivity,
			false,
			"Record activity while connections are open",
		),
	)
	return sshCmd
}

// Run runs the command logic.
func (cmd *SSHServerCmd) Run(c *cobra.Command, _ []string) error {
	server, err := sshserver.NewContainerServer(cmd.Address, cmd.Workdir)
	if err != nil {
		return err
	}

	// check if ssh is already running at that port
	available, err := port.IsAvailable(cmd.Address)
	if !available {
		if err != nil {
			return fmt.Errorf("address %s already in use: %w", cmd.Address, err)
		}

		log.Infof("address %s already in use", cmd.Address)
		return nil
	}

	if !cmd.TrackActivity {
		return server.ListenAndServe()
	}

	ctx := c.Context()
	listener, err := net.Listen("tcp", cmd.Address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cmd.Address, err)
	}
	stopClosing := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopClosing()
	defer func() { _ = listener.Close() }()
	err = server.Serve(trackConnections(listener, func() (context.Context, func(), error) {
		return startConnectionActivity(ctx)
	}))
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return err
}

func startConnectionActivity(ctx context.Context) (context.Context, func(), error) {
	return (agentd.ActivityWriter{
		Path: pkgconfig.ContainerActivityFile, LifecycleDir: pkgconfig.ContainerLifecycleDir,
		RequireOwner: true,
	}).Start(ctx)
}

// trackConnections records activity from the moment a connection is accepted
// until it closes. The idle listener itself is not activity.
func trackConnections(l net.Listener, start func() (context.Context, func(), error)) net.Listener {
	return &activityListener{Listener: l, start: start}
}

type activityListener struct {
	net.Listener
	start func() (context.Context, func(), error)
}

func (l *activityListener) Accept() (net.Conn, error) {
	reportedFailure := false
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ctx, stop, err := l.start()
		if err != nil {
			_ = conn.Close()
			if !reportedFailure {
				log.Debugf("reject SSH connection without activity registration: %v", err)
				reportedFailure = true
			}
			continue
		}
		tracked := &activityConn{Conn: conn, stop: stop}
		go func() { <-ctx.Done(); _ = tracked.Close() }()
		return tracked, nil
	}
}

type activityConn struct {
	net.Conn
	stop func()
	once sync.Once
}

func (c *activityConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.stop)
	return err
}
