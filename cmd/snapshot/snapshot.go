package snapshot

import (
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
)

func NewSnapshotCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage workspace snapshots",
	}
	cmd.AddCommand(NewCreateCmd(globalFlags))
	cmd.AddCommand(NewListCmd(globalFlags))
	cmd.AddCommand(NewRestoreCmd(globalFlags))
	cmd.AddCommand(NewDeleteCmd(globalFlags))
	return cmd
}

func resolveRegistry(flagValue string, devsyConfig *config.Config) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if v := devsyConfig.ContextOption(config.ContextOptionSnapshotRegistry); v != "" {
		return v, nil
	}
	return "", fmt.Errorf(
		"no snapshot registry configured: pass --registry or set SNAPSHOT_REGISTRY " +
			"(devsy context set -o SNAPSHOT_REGISTRY=<registry>)",
	)
}
