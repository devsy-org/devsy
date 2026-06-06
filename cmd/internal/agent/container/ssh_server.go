package container

import (
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/log"
	helperssh "github.com/devsy-org/devsy/pkg/ssh/server"
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

	sshCmd.Flags().
		StringVar(&cmd.Address, "address", fmt.Sprintf("127.0.0.1:%d", helperssh.DefaultUserPort), "Address to listen to")
	sshCmd.Flags().
		StringVar(&cmd.RemoteUser, "remote-user", "", "The remote user for this workspace")
	sshCmd.Flags().
		StringVar(&cmd.Workdir, "workdir", "", "Directory where commands will run on the host")
	return sshCmd
}

// Run runs the command logic.
func (cmd *SSHServerCmd) Run(_ *cobra.Command, _ []string) error {
	server, err := helperssh.NewContainerServer(cmd.Address, cmd.Workdir)
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
