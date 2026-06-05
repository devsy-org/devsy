package container

import (
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/devsyconfig"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

type SetupDevsyPlatformAccessCmd struct {
	*flags.GlobalFlags
}

// NewSetupDevsyPlatformAccessCmd creates a new setup-devsy-platform-access command.
// This agent command injects Devsy Platform configuration from the local machine into the workspace.
func NewSetupDevsyPlatformAccessCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetupDevsyPlatformAccessCmd{
		GlobalFlags: flags,
	}

	return &cobra.Command{
		Use:   "setup-devsy-platform-access",
		Short: "used to setup Devsy Platform access",
		RunE:  cmd.Run,
	}
}

// Run fetches Devsy Platform credentials from the credentials server and sets them up inside the workspace.
func (c *SetupDevsyPlatformAccessCmd) Run(_ *cobra.Command, args []string) error {
	port, err := credentials.GetPort()
	if err != nil {
		return fmt.Errorf("get port: %w", err)
	}

	cfg, err := devsyconfig.GetDevsyConfig(port)
	if err != nil {
		return err
	}

	if cfg == nil {
		log.Debug("Got empty devsy config response, Devsy Platform access won't be set up.")
		return nil
	}

	if err := devsyconfig.AuthDevsyCliToPlatform(cfg); err != nil {
		// log error but don't return to allow other CLIs to install as well
		log.Warnf("unable to authenticate devsy cli: %v", err)
	}

	if err := devsyconfig.AuthVClusterCliToPlatform(cfg); err != nil {
		// log error but don't return to allow other CLIs to install as well
		log.Warnf("unable to authenticate vcluster cli: %v", err)
	}

	return nil
}
