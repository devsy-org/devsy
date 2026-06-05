package helper

import (
	"bytes"
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

type GetProviderNameCmd struct {
	*flags.GlobalFlags
}

// NewGetProviderNameCmd creates a new command.
func NewGetProviderNameCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &GetProviderNameCmd{
		GlobalFlags: flags,
	}
	shellCmd := &cobra.Command{
		Use:   "get-provider-name",
		Short: "Retrieves a provider name",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return shellCmd
}

func (cmd *GetProviderNameCmd) Run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("provider is missing")
	}

	providerRaw, _, err := provider.ResolveProvider(args[0])
	if err != nil {
		return fmt.Errorf("resolve provider: %w", err)
	}

	providerConfig, err := provider.ParseProvider(bytes.NewReader(providerRaw))
	if err != nil {
		return fmt.Errorf("parse provider: %w", err)
	}

	fmt.Print(providerConfig.Name)
	return nil
}
