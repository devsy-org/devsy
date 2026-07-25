package secrets

import (
	"context"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

type AttachCmd struct {
	*flags.GlobalFlags
}

func NewAttachCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &AttachCmd{GlobalFlags: flags}
	attachCmd := &cobra.Command{
		Use:   "attach NAME",
		Short: "Bind a secret to the active context so it is injected automatically on up",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	return attachCmd
}

func verifySensitive(devsyConfig *config.Config, contextName, name string) error {
	store, err := secrets.NewStoreForConfig(devsyConfig)
	if err != nil {
		return err
	}
	meta, err := store.Meta(contextName, name)
	if err != nil {
		return fmt.Errorf("cannot attach secret %q: %w", name, err)
	}
	if !meta.Sensitive() {
		return fmt.Errorf("%q is an environment variable; use \"devsy env\"", name)
	}
	return nil
}

func (cmd *AttachCmd) Run(_ context.Context, name string) error {
	if err := secrets.ValidateName(name); err != nil {
		return err
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	contextName := devsyConfig.DefaultContext

	if err := verifySensitive(devsyConfig, contextName, name); err != nil {
		return err
	}

	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil {
		return fmt.Errorf("context %q doesn't exist", contextName)
	}
	if slices.Contains(ctxConfig.Secrets, name) {
		log.Infof("secret %q already attached to context %q", name, contextName)
		return nil
	}
	ctxConfig.Secrets = append(ctxConfig.Secrets, name)

	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	log.Infof("secret %q attached to context %q", name, contextName)
	return nil
}

type DetachCmd struct {
	*flags.GlobalFlags
}

func NewDetachCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DetachCmd{GlobalFlags: flags}
	detachCmd := &cobra.Command{
		Use:   "detach NAME",
		Short: "Unbind a secret from the active context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	return detachCmd
}

func (cmd *DetachCmd) Run(_ context.Context, name string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	contextName := devsyConfig.DefaultContext

	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil {
		return fmt.Errorf("context %q doesn't exist", contextName)
	}

	idx := slices.Index(ctxConfig.Secrets, name)
	if idx < 0 {
		log.Infof("secret %q is not attached to context %q", name, contextName)
		return nil
	}
	ctxConfig.Secrets = slices.Delete(ctxConfig.Secrets, idx, idx+1)

	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	log.Infof("secret %q detached from context %q", name, contextName)
	return nil
}
