package context

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
)

// DeleteCmd holds the delete cmd flags.
type DeleteCmd struct {
	*flags.GlobalFlags
}

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
func (cmd *DeleteCmd) Run(_ context.Context, contextName string) error {
	if contextName == "" && cmd.GlobalFlags != nil {
		contextName = cmd.Context
	}
	unlock, err := config.LockConfigForContextDeletion(contextName)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := config.LoadConfig("", "")
	if err != nil {
		return err
	}
	intent, err := config.ReadContextDeletionIntent()
	if err != nil {
		return err
	}
	if intent != nil {
		return resumeContextDeletion(cfg, intent)
	}
	return deleteRegisteredContext(cfg, contextName)
}

func deleteRegisteredContext(cfg *config.Config, contextName string) error {
	if contextName == "" {
		contextName = cfg.DefaultContext
	}
	if cfg.Contexts[contextName] == nil {
		return fmt.Errorf("context %q doesn't exist", contextName)
	}
	if contextName == config.DefaultContext {
		return fmt.Errorf("cannot delete 'default' context")
	}
	request, err := newContextDeleteRequest(cfg, contextName)
	if err != nil {
		return err
	}
	return deleteContextValues(request)
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
