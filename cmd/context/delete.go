package context

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

// DeleteCmd holds the delete cmd flags.
type DeleteCmd struct {
	*flags.GlobalFlags
}

// NewDeleteCmd deletes a new command.
func NewDeleteCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{
		GlobalFlags: flags,
	}
	deleteCmd := &cobra.Command{
		Use:     "delete",
		Aliases: []string{"rm"},
		Short:   "Delete a Devsy context",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("specify the context to delete")
			}

			devsyContext := ""
			if len(args) == 1 {
				devsyContext = args[0]
			}

			return cmd.Run(cobraCmd.Context(), devsyContext)
		},
	}

	return deleteCmd
}

// Run runs the command logic.
func (cmd *DeleteCmd) Run(ctx context.Context, context string) error {
	devsyConfig, err := config.LoadConfig(context, cmd.Provider)
	if err != nil {
		return err
	}

	if context == "" {
		context = devsyConfig.DefaultContext
	} else if devsyConfig.Contexts[context] == nil {
		return fmt.Errorf("context %q doesn't exist", context)
	}

	if context == "default" {
		return fmt.Errorf("cannot delete 'default' context")
	}

	if err := deleteContextSecrets(devsyConfig, context); err != nil {
		return err
	}

	delete(devsyConfig.Contexts, context)
	resetContextReferences(devsyConfig, context)

	err = config.SaveConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return removeContextDir(context)
}

// removeContextDir removes the directory for a deleted context.
// This runs last, after config.yaml is already saved without the context, so
// a failure here leaves the context unregistered even though some
// disk cleanup may need a manual retry.
func removeContextDir(contextName string) error {
	dir, err := config.DefaultPathManager().ContextDir(contextName)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete context dir: %w", err)
	}
	return nil
}

func resetContextReferences(devsyConfig *config.Config, context string) {
	if devsyConfig.DefaultContext == context {
		devsyConfig.DefaultContext = "default"
	}
	if devsyConfig.OriginalContext == context {
		devsyConfig.OriginalContext = "default"
	}
}

// deleteContextSecrets aborts (rather than orphaning stored values) if the store
// is unavailable or a delete fails, so the deletion can be retried intact.
func deleteContextSecrets(devsyConfig *config.Config, contextName string) error {
	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil || len(ctxConfig.Secrets) == 0 {
		return nil
	}

	store, err := secrets.NewStoreForConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("open secrets store for context %q: %w", contextName, err)
	}
	for _, name := range ctxConfig.Secrets {
		if err := store.Delete(contextName, name); err != nil {
			return fmt.Errorf("delete secret %q for context %q: %w", name, contextName, err)
		}
	}
	return nil
}
