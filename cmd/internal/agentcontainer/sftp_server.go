package agentcontainer

import (
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/pkg/ssh/server"
	"github.com/spf13/cobra"
)

func NewSFTPServerCmd() *cobra.Command {
	var home string
	cmd := &cobra.Command{
		Use:    "sftp-server",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if home == "" {
				return fmt.Errorf("home directory is required")
			}
			return server.ServeSFTP(sftpStdio{Reader: os.Stdin, Writer: os.Stdout}, home)
		},
	}
	cmd.Flags().StringVar(&home, "home", "", "Home directory for SFTP paths")
	return cmd
}

type sftpStdio struct {
	io.Reader
	io.Writer
}

func (sftpStdio) Close() error {
	return nil
}
