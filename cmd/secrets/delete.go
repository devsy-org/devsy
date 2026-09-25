package secrets

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	devsysecrets "github.com/devsy-org/devsy/pkg/secrets"
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
	store, err := devsysecrets.NewStoreForConfig(devsyConfig)
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

	if err := deleteSecretValue(deleteSecretRequest{
		config:  devsyConfig,
		store:   store,
		context: contextName,
		name:    name,
		save:    config.SaveConfig,
	}); err != nil {
		return err
	}

	log.Infof("secret %q deleted from context %q", name, contextName)
	return nil
}

type deleteSecretRequest struct {
	config  *config.Config
	store   devsysecrets.Store
	context string
	name    string
	save    func(*config.Config) error
}

type removedSecretBinding struct {
	attached bool
	index    int
}

func deleteSecretValue(request deleteSecretRequest) error {
	devsyConfig := request.config
	store := request.store
	contextName := request.context
	name := request.name
	saveConfig := request.save
	var binding removedSecretBinding
	if ctxConfig := devsyConfig.Contexts[contextName]; ctxConfig != nil {
		if idx := slices.Index(ctxConfig.Secrets, name); idx >= 0 {
			binding = removedSecretBinding{attached: true, index: idx}
			ctxConfig.Secrets = slices.Delete(ctxConfig.Secrets, idx, idx+1)
			if err := saveConfig(devsyConfig); err != nil {
				return err
			}
		}
	}

	deleteErr := store.Delete(contextName, name)
	if deleteErr == nil || !binding.attached {
		return deleteErr
	}

	if _, err := store.Get(contextName, name); err != nil {
		return deleteErr
	}

	ctxConfig := devsyConfig.Contexts[contextName]
	ctxConfig.Secrets = append(ctxConfig.Secrets, "")
	copy(ctxConfig.Secrets[binding.index+1:], ctxConfig.Secrets[binding.index:])
	ctxConfig.Secrets[binding.index] = name
	if rollbackErr := saveConfig(devsyConfig); rollbackErr != nil {
		return errors.Join(
			deleteErr,
			fmt.Errorf("restore secret attachment after failed delete: %w", rollbackErr),
		)
	}

	return deleteErr
}
