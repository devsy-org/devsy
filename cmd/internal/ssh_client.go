package cmdinternal

import (
	"context"
	"fmt"
	"os"

	command2 "github.com/devsy-org/devsy/pkg/command"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

type SSHClient struct {
	Address string
	KeyFile string
	User    string
}

// NewSSHClientCmd creates a new ssh command.
func NewSSHClientCmd() *cobra.Command {
	cmd := &SSHClient{}
	sshCmd := &cobra.Command{
		Use:   "ssh-client",
		Short: "Starts a new ssh client session",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	cliflags.Add(sshCmd,
		cliflags.String(&cmd.KeyFile, names.KeyFile, "", "SSH Key file to use"),
		cliflags.String(&cmd.Address, names.Address, "", "Address to connect to"),
		cliflags.String(&cmd.User, names.User, "root", "User to connect as"),
	)
	_ = sshCmd.MarkFlagRequired(names.Address)
	return sshCmd
}

func (cmd *SSHClient) Run(ctx context.Context, args []string) error {
	sshConfig, err := cmd.getConfig()
	if err != nil {
		return err
	}

	sshClient, err := ssh.Dial("tcp", cmd.Address, sshConfig)
	if err != nil {
		return err
	}
	defer func() { _ = sshClient.Close() }()

	sess, err := sshClient.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	err = sess.Run(command2.Quote(args))
	if err != nil {
		return err
	}

	return nil
}

func (cmd *SSHClient) getConfig() (*ssh.ClientConfig, error) {
	clientConfig := &ssh.ClientConfig{
		User:            cmd.User,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// key file authentication?
	if cmd.KeyFile != "" {
		out, err := os.ReadFile(cmd.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("read private ssh key: %w", err)
		}

		signer, err := ssh.ParsePrivateKey(out)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}

		clientConfig.Auth = append(clientConfig.Auth, ssh.PublicKeys(signer))
	}

	return clientConfig, nil
}
