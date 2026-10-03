package context

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

// DeleteCmd holds the delete cmd flags.
type DeleteCmd struct {
	*flags.GlobalFlags
}

const (
	environmentStoreUnavailable = "environment store is unavailable"
	environmentStoreNotWritable = "environment store is not writable"
)

// NewDeleteCmd creates a new command.
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
	err := config.UpdateConfig(context, cmd.Provider, func(devsyConfig *config.Config) error {
		if context == "" {
			context = devsyConfig.DefaultContext
		} else if devsyConfig.Contexts[context] == nil {
			return fmt.Errorf("context %q doesn't exist", context)
		}

		if context == config.DefaultContext {
			return fmt.Errorf("cannot delete 'default' context")
		}

		// Read and validate environment state before deleting any secrets. A
		// corrupt environment store must not leave the context's secrets gone
		// while the context itself remains registered.
		if err := preflightContextEnvironment(devsyConfig, context); err != nil {
			return err
		}

		if err := deleteContextSecrets(devsyConfig, context); err != nil {
			return err
		}

		if err := deleteContextEnvironment(devsyConfig, context); err != nil {
			return err
		}

		delete(devsyConfig.Contexts, context)
		resetContextReferences(devsyConfig, context)
		return nil
	})
	if err != nil {
		return err
	}

	return removeContextDir(context)
}

func preflightContextEnvironment(devsyConfig *config.Config, contextName string) error {
	store, err := envstore.NewStoreForConfig(devsyConfig)
	if err != nil {
		return environmentPreflightError(contextName, environmentStoreUnavailable)
	}
	if _, err := store.List(contextName); err != nil {
		// Do not include parser or store errors here: they may contain
		// environment values from the plaintext file.
		return environmentPreflightError(contextName, environmentStoreUnavailable)
	}

	path, err := config.GetConfigPath()
	if err != nil {
		return environmentPreflightError(contextName, environmentStoreUnavailable)
	}
	probe, err := os.CreateTemp(filepath.Dir(path), ".devsy-env-delete-check-*")
	if err != nil {
		return environmentPreflightError(contextName, environmentStoreNotWritable)
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return environmentPreflightError(contextName, environmentStoreNotWritable)
	}
	if err := os.Remove(probePath); err != nil {
		return environmentPreflightError(contextName, environmentStoreNotWritable)
	}
	return nil
}

func environmentPreflightError(contextName, detail string) error {
	return fmt.Errorf("preflight environment cleanup for context %q: %s", contextName, detail)
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
		devsyConfig.DefaultContext = config.DefaultContext
	}
	if devsyConfig.OriginalContext == context {
		devsyConfig.OriginalContext = config.DefaultContext
	}
}

// deleteContextSecrets aborts (rather than orphaning stored values) if the store
// is unavailable or a delete fails, so the deletion can be retried intact.
func deleteContextSecrets(devsyConfig *config.Config, contextName string) error {
	ctxConfig := devsyConfig.Contexts[contextName]
	if ctxConfig == nil || len(ctxConfig.Secrets) == 0 {
		return nil
	}

	store, err := secrets.NewSecretStoreForConfig(devsyConfig)
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

// deleteContextEnvironment removes every managed env value, including detached
// values. Listing the dedicated plaintext store never opens secret backends.
func deleteContextEnvironment(devsyConfig *config.Config, contextName string) error {
	store, err := envstore.NewStoreForConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("open environment store for context %q: %w", contextName, err)
	}
	values, err := store.List(contextName)
	if err != nil {
		return fmt.Errorf("list environment values for context %q: %w", contextName, err)
	}
	for _, value := range values {
		if err := store.Delete(contextName, value.Name); err != nil {
			return fmt.Errorf(
				"delete environment variable %q for context %q: %w",
				value.Name,
				contextName,
				err,
			)
		}
	}
	return nil
}
