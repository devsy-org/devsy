package container

import (
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/loftconfig"
	"github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

type SetupLoftPlatformAccessCmd struct {
	*flags.GlobalFlags

	Context  string
	Provider string
	Port     int
}

// NewSetupLoftPlatformAccessCmd creates a new setup-loft-platform-access command
// This agent command can be used to inject loft platform configuration from local machine to workspace.
func NewSetupLoftPlatformAccessCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetupLoftPlatformAccessCmd{
		GlobalFlags: flags,
	}

	setupLoftPlatformAccessCmd := &cobra.Command{
		Use:   "setup-loft-platform-access",
		Short: "used to setup Devsy Platform access",
		RunE:  cmd.Run,
	}

	setupLoftPlatformAccessCmd.Flags().StringVar(&cmd.Context, "context", "", "context to use")
	_ = setupLoftPlatformAccessCmd.Flags().
		MarkDeprecated("context", "Information should be provided by services server, don't use this flag anymore")

	setupLoftPlatformAccessCmd.Flags().StringVar(&cmd.Provider, "provider", "", "provider to use")
	_ = setupLoftPlatformAccessCmd.Flags().
		MarkDeprecated("provider", "Information should be provided by services server, don't use this flag anymore")

	setupLoftPlatformAccessCmd.Flags().
		IntVar(&cmd.Port, "port", 0, "If specified, will use the given port")
	_ = setupLoftPlatformAccessCmd.Flags().MarkDeprecated("port", "")

	return setupLoftPlatformAccessCmd
}

// Run executes main command logic.
// It fetches Devsy Platform credentials from credentials server and sets it up inside the workspace.
func (c *SetupLoftPlatformAccessCmd) Run(_ *cobra.Command, args []string) error {
	logger := log.Default.ErrorStreamOnly()

	port, err := credentials.GetPort()
	if err != nil {
		return fmt.Errorf("get port: %w", err)
	}
	// backwards compatibility, remove in future release
	if c.Port > 0 {
		port = c.Port
	}

	loftConfig, err := devsyconfig.GetDevsyConfig(c.Context, c.Provider, port, logger)
	if err != nil {
		return err
	}

	if loftConfig == nil {
		logger.Debug("Got empty devsy config response, Devsy Platform access won't be set up.")
		return nil
	}

	err = devsyconfig.AuthDevsyCliToPlatform(loftConfig, logger)
	if err != nil {
		// log error but don't return to allow other CLIs to install as well
		logger.Warnf("unable to authenticate devsy cli: %w", err)
	}

	err = devsyconfig.AuthVClusterCliToPlatform(loftConfig, logger)
	if err != nil {
		// log error but don't return to allow other CLIs to install as well
		logger.Warnf("unable to authenticate vcluster cli: %w", err)
	}

	return nil
}
