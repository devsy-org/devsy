package env

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

type AttachCmd struct{ *flags.GlobalFlags }

func NewAttachCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &AttachCmd{GlobalFlags: flags}
	return &cobra.Command{
		Use:   "attach NAME",
		Short: "Bind a non-sensitive environment variable to the active context",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return cmd.Run(c.Context(), args[0])
		},
	}
}

func (cmd *AttachCmd) Run(_ context.Context, name string) error {
	if err := secrets.ValidateName(name); err != nil {
		return err
	}
	unlock, err := config.LockConfig()
	if err != nil {
		return err
	}
	defer unlock()
	devsyConfig, err := config.LoadConfig(cmd.Context, "")
	if err != nil {
		return err
	}
	contextName := devsyConfig.DefaultContext
	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil {
		return fmt.Errorf("context %q doesn't exist", contextName)
	}
	if err := verifyAttachableEnv(devsyConfig, contextName, name); err != nil {
		return err
	}
	if slices.Contains(ctxConfig.EnvVars, name) {
		log.Infof("environment variable %q already attached to context %q", name, contextName)
		return nil
	}
	ctxConfig.EnvVars = append(ctxConfig.EnvVars, name)
	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	log.Infof("environment variable %q attached to context %q", name, contextName)
	return nil
}

func verifyAttachableEnv(devsyConfig *config.Config, contextName, name string) error {
	store, err := secrets.NewStoreForConfig(devsyConfig)
	if err != nil {
		return err
	}
	meta, err := store.Meta(contextName, name)
	if err != nil {
		return fmt.Errorf("cannot attach environment variable %q: %w", name, err)
	}
	if meta.Sensitive() {
		return fmt.Errorf("%q is a secret; use \"devsy secret\"", name)
	}
	return nil
}

type DetachCmd struct{ *flags.GlobalFlags }

func NewDetachCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DetachCmd{GlobalFlags: flags}
	return &cobra.Command{
		Use:   "detach NAME",
		Short: "Unbind an environment variable from the active context",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return cmd.Run(c.Context(), args[0])
		},
	}
}

func (cmd *DetachCmd) Run(_ context.Context, name string) error {
	if err := secrets.ValidateName(name); err != nil {
		return err
	}
	unlock, err := config.LockConfig()
	if err != nil {
		return err
	}
	defer unlock()
	devsyConfig, err := config.LoadConfig(cmd.Context, "")
	if err != nil {
		return err
	}
	contextName := devsyConfig.DefaultContext
	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil {
		return fmt.Errorf("context %q doesn't exist", contextName)
	}
	idx := slices.Index(ctxConfig.EnvVars, name)
	if idx < 0 {
		log.Infof("environment variable %q is not attached to context %q", name, contextName)
		return nil
	}
	ctxConfig.EnvVars = slices.Delete(ctxConfig.EnvVars, idx, idx+1)
	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	log.Infof("environment variable %q detached from context %q", name, contextName)
	return nil
}
