package env

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

type DeleteCmd struct {
	*flags.GlobalFlags
}

func NewDeleteCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{GlobalFlags: flags}
	deleteCmd := &cobra.Command{
		Use:     "delete NAME",
		Aliases: []string{"rm"},
		Short:   "Delete an environment variable from the active context",
		Args:    cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}
	return deleteCmd
}

func (cmd *DeleteCmd) Run(_ context.Context, name string) error {
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
	store, err := secrets.NewStoreForConfig(devsyConfig)
	if err != nil {
		return err
	}
	meta, err := store.Meta(contextName, name)
	if err != nil {
		return err
	}
	if meta.Sensitive() {
		return fmt.Errorf("%q is a secret; use \"devsy secret delete\"", name)
	}
	if err := deleteEnvironmentValue(deleteEnvRequest{
		config:  devsyConfig,
		store:   store,
		context: contextName,
		name:    name,
		save:    config.SaveConfig,
	}); err != nil {
		return err
	}
	log.Infof("env var %q deleted from context %q", name, contextName)
	return nil
}

type deleteEnvRequest struct {
	config  *config.Config
	store   secrets.Store
	context string
	name    string
	save    func(*config.Config) error
}

type removedEnvBinding struct {
	attached bool
	index    int
}

func deleteEnvironmentValue(request deleteEnvRequest) error {
	devsyConfig := request.config
	store := request.store
	contextName := request.context
	name := request.name
	saveConfig := request.save
	var binding removedEnvBinding
	if ctxConfig := devsyConfig.Contexts[contextName]; ctxConfig != nil {
		if idx := slices.Index(ctxConfig.EnvVars, name); idx >= 0 {
			binding = removedEnvBinding{attached: true, index: idx}
			ctxConfig.EnvVars = slices.Delete(ctxConfig.EnvVars, idx, idx+1)
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
	ctxConfig.EnvVars = append(ctxConfig.EnvVars, "")
	copy(ctxConfig.EnvVars[binding.index+1:], ctxConfig.EnvVars[binding.index:])
	ctxConfig.EnvVars[binding.index] = name
	if rollbackErr := saveConfig(devsyConfig); rollbackErr != nil {
		rollbackMessage := "restore environment variable attachment after failed delete: %w"
		return errors.Join(
			deleteErr,
			fmt.Errorf(rollbackMessage, rollbackErr),
		)
	}

	return deleteErr
}
