package env

import (
	"context"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/internal/managedvalue"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/log"
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
	if err := envstore.ValidateName(name); err != nil {
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
	store, err := envstore.NewStoreForConfig(devsyConfig)
	if err != nil {
		return err
	}
	_, err = store.Meta(contextName, name)
	if err != nil {
		return err
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
	store   interface{ Delete(string, string) error }
	context string
	name    string
	save    func(*config.Config) error
}

func deleteEnvironmentValue(request deleteEnvRequest) error {
	return managedvalue.Delete(managedvalue.DeleteRequest{
		Config: request.config, Store: request.store, Context: request.context,
		Name: request.name, Binding: managedvalue.EnvBinding, Save: request.save,
	})
}
