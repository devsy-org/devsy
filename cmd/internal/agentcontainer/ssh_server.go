package agentcontainer

import (
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
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

	return server.ListenAndServe()
}
