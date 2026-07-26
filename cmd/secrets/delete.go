package secrets

import (
	"context"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

type DeleteCmd struct {
	*flags.GlobalFlags
}

func NewDeleteCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{
		GlobalFlags: flags,
	}
	deleteCmd := &cobra.Command{
		Use:     "delete NAME",
		Aliases: []string{"rm"},
		Short:   "Delete a secret from the active context",
		Args:    cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	return deleteCmd
}

func (cmd *DeleteCmd) Run(_ context.Context, name string) error {
	contextName, store, err := resolveContext(cmd.GlobalFlags)
	if err != nil {
		return err
	}

	meta, err := store.Meta(contextName, name)
	if err != nil {
		return err
	}
	if !meta.Sensitive() {
		return fmt.Errorf("%q is an environment variable; use \"devsy env delete\"", name)
	}

	if err := store.Delete(contextName, name); err != nil {
		return err
	}
	if err := unbindFromContext(cmd.GlobalFlags, contextName, name); err != nil {
		return err
	}

	log.Infof("secret %q deleted from context %q", name, contextName)
	return nil
}

// unbindFromContext removes a deleted secret from its context's attached list so
// a stale binding is not left pointing at a now-missing secret.
func unbindFromContext(globalFlags *flags.GlobalFlags, contextName, name string) error {
	devsyConfig, err := config.LoadConfig(globalFlags.Context, globalFlags.Provider)
	if err != nil {
		return err
	}
	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil {
		return nil
	}
	idx := slices.Index(ctxConfig.Secrets, name)
	if idx < 0 {
		return nil
	}
	ctxConfig.Secrets = slices.Delete(ctxConfig.Secrets, idx, idx+1)

	return config.SaveConfig(devsyConfig)
}
