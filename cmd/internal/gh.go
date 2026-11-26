package cmdinternal

import (
	"encoding/json"
	"os"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ghcredentials"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

func NewGHCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "gh",
		Hidden:             true,
		DisableFlagParsing: true,
		PersistentPreRunE:  ghPreRunE,
		Run:                func(_ *cobra.Command, args []string) { os.Exit(ghcredentials.Run(args)) },
	}
}

func NewGHBackendCmd() *cobra.Command {
	return &cobra.Command{
		Use: "gh-backend", Hidden: true, Args: cobra.NoArgs,
		PersistentPreRunE: ghPreRunE,
		RunE: func(c *cobra.Command, _ []string) error {
			return json.NewEncoder(c.OutOrStdout()).Encode(ghcredentials.DiscoverBackend())
		},
	}
}

func ghPreRunE(*cobra.Command, []string) error {
	log.Init(log.Config{Level: os.Getenv(config.EnvLogLevel), DefaultLevel: log.DefaultLevel})
	return nil
}
