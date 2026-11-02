package cmdinternal

import (
	"errors"
	"os"
	"strconv"

	"github.com/devsy-org/devsy/pkg/ghcredentials"
	"github.com/spf13/cobra"
)

func NewInstallGHWrapperCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "install-gh-wrapper <port>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			port, err := strconv.Atoi(args[0])
			if err != nil || port < 1 || port > 65535 {
				return errors.New("invalid credentials server port")
			}
			if os.Geteuid() != 0 {
				return errors.New("GitHub CLI wrapper installation requires root")
			}
			return ghcredentials.InstallPrepared(c.Context(), port)
		},
	}
}
