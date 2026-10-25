package agentcontainer

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/devsy-org/devsy/cmd/flags"
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
func (cmd *SSHServerCmd) Run(_ *cobra.Command, _ []string) error {
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

	listener, err := net.Listen("tcp", cmd.Address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cmd.Address, err)
	}
	return server.Serve(trackConnections(listener, startConnectionActivity))
}

func startConnectionActivity() (stop func()) {
	return agentd.StartActivityWriter(context.Background(), agentd.ActivityIntervalForLifecycle())
}

// trackConnections records activity from the moment a connection is accepted
// until it closes. The idle listener itself is not activity.
func trackConnections(l net.Listener, start func() (stop func())) net.Listener {
	return &activityListener{Listener: l, start: start}
}

type activityListener struct {
	net.Listener
	start func() (stop func())
}

func (l *activityListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &activityConn{Conn: conn, stop: l.start()}, nil
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
