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
		Short: "Bind a secret reference to the active context so it is injected automatically on up",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	return attachCmd
}

func verifyLocalSensitive(devsyConfig *config.Config, contextName, name string) error {
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

func verifySecretReference(
	ctx context.Context,
	devsyConfig *config.Config,
	ref secrets.SecretRef,
) error {
	if ref.Source == secrets.LocalSourceName &&
		(ref.Type == "" || ref.Type == secrets.LocalSourceName) {
		return verifyLocalSensitive(devsyConfig, devsyConfig.DefaultContext, ref.Name)
	}
	resolver, err := secrets.NewResolverForConfig(devsyConfig)
	if err != nil {
		return err
	}
	resolved, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return fmt.Errorf("cannot attach secret %q: %w", ref.String(), err)
	}
	if !resolved.Sensitive {
		return fmt.Errorf("%q is not a sensitive secret", ref.String())
	}
	return nil
}

func (cmd *AttachCmd) Run(ctx context.Context, name string) error {
	ref, err := secrets.ParseRef(name)
	if err != nil {
		return err
	}
	canonical := ref.String()

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	contextName := devsyConfig.DefaultContext

	if err := verifySecretReference(ctx, devsyConfig, ref); err != nil {
		return err
	}

	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil {
		return fmt.Errorf("context %q doesn't exist", contextName)
	}
	if slices.Contains(ctxConfig.Secrets, canonical) {
		log.Infof("secret %q already attached to context %q", canonical, contextName)
		return nil
	}
	ctxConfig.Secrets = append(ctxConfig.Secrets, canonical)

	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	log.Infof("secret %q attached to context %q", canonical, contextName)
	return nil
}

type DetachCmd struct {
	*flags.GlobalFlags
}

func NewDetachCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DetachCmd{GlobalFlags: flags}
	detachCmd := &cobra.Command{
		Use:   "detach NAME",
		Short: "Unbind a secret reference from the active context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	return detachCmd
}

func (cmd *DetachCmd) Run(_ context.Context, name string) error {
	ref, err := secrets.ParseRef(name)
	if err != nil {
		return err
	}
	canonical := ref.String()
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	contextName := devsyConfig.DefaultContext

	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil {
		return fmt.Errorf("context %q doesn't exist", contextName)
	}

	idx := slices.Index(ctxConfig.Secrets, canonical)
	if idx < 0 {
		log.Infof("secret %q is not attached to context %q", canonical, contextName)
		return nil
	}
	ctxConfig.Secrets = slices.Delete(ctxConfig.Secrets, idx, idx+1)

	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	log.Infof("secret %q detached from context %q", canonical, contextName)
	return nil
}
