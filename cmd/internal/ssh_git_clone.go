package cmdinternal

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	command2 "github.com/devsy-org/devsy/pkg/command"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

type SSHGitClone struct {
	KeyFiles []string
	Port     string
}

func NewSSHGitCloneCmd() *cobra.Command {
	cmd := &SSHGitClone{}
	sshCmd := &cobra.Command{
		Use:   "ssh-git-clone",
		Short: "Drop-in ssh replacement in GIT_SSH_COMMAND",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	cliflags.Add(sshCmd,
		cliflags.StringArray(&cmd.KeyFiles, names.KeyFile, []string{}, "SSH Key file to use"),
		cliflags.String(&cmd.Port, names.Port, "22", "SSH port to use, defaults to 22"),
	)
	_ = sshCmd.MarkFlagRequired(names.KeyFile)
	return sshCmd
}

func (cmd *SSHGitClone) Run(ctx context.Context, args []string) error {
	host, sshCmdArgs, err := parseSSHArgs(args)
	if err != nil {
		return err
	}

	user, addr, err := parseSSHHost(host)
	if err != nil {
		return err
	}

	sshConfig, err := getConfig(user, cmd.KeyFiles)
	if err != nil {
		return err
	}

	return runSSHSession(sshConfig, net.JoinHostPort(addr, cmd.Port), sshCmdArgs)
}

func parseSSHArgs(args []string) (host string, sshCmdArgs []string, err error) {
	if len(args) < 2 {
		return "", nil, fmt.Errorf(
			"expected args in format: {user}@{host} {commands...}, received %q",
			strings.Join(args, " "),
		)
	}
	host = args[0]
	sshCmdArgs = args[1:]
	if len(host) == 0 || len(sshCmdArgs) == 0 {
		return "", nil, fmt.Errorf(
			"unexpected input: host: %s, args: %s",
			host,
			strings.Join(sshCmdArgs, " "),
		)
	}

	return host, sshCmdArgs, nil
}

func runSSHSession(sshConfig *ssh.ClientConfig, addr string, sshCmdArgs []string) error {
	sshClient, err := ssh.Dial("tcp", addr, sshConfig)
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

	return sess.Run(command2.Quote(sshCmdArgs))
}

func getConfig(userName string, keyFilePaths []string) (*ssh.ClientConfig, error) {
	signers := []ssh.Signer{}
	for _, keyFilePath := range keyFilePaths {
		out, err := os.ReadFile(keyFilePath)
		if err != nil {
			return nil, fmt.Errorf("read private ssh key: %w", err)
		}

		signer, err := ssh.ParsePrivateKey(out)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}

		signers = append(signers, signer)
	}

	return &ssh.ClientConfig{
		User:            userName,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}, nil
}

func parseSSHHost(host string) (string, string, error) {
	s := strings.SplitN(host, "@", 2)
	if len(s) != 2 {
		return "", "", fmt.Errorf("split host: %s", host)
	}

	return s[0], s[1], nil
}
