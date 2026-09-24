package env

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

type SetCmd struct {
	*flags.GlobalFlags

	Value    string
	valueSet bool
}

func NewSetCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetCmd{GlobalFlags: flags}
	setCmd := &cobra.Command{
		Use:   "set NAME=VALUE | set NAME --value VALUE",
		Short: "Create or update an environment variable",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cmd.valueSet = cobraCmd.Flags().Changed(names.Value)
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}
	cliflags.Add(
		setCmd,
		cliflags.String(&cmd.Value, names.Value, "", "The value (alternative to NAME=VALUE)"),
	)
	return setCmd
}

func (cmd *SetCmd) Run(_ context.Context, arg string) error {
	name, value := arg, cmd.Value
	if k, v, ok := strings.Cut(arg, "="); ok {
		if cmd.valueSet {
			return fmt.Errorf("specify the value inline as NAME=VALUE or with --value, not both")
		}
		name, value = k, v
	}
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
	if meta, metaErr := store.Meta(contextName, name); metaErr == nil {
		ctxConfig := devsyConfig.Contexts[contextName]
		if meta.Sensitive() && ctxConfig != nil && slices.Contains(ctxConfig.Secrets, name) {
			return fmt.Errorf(
				"%q is attached as a secret; detach it before converting it to an environment variable",
				name,
			)
		}
	} else if !errors.Is(metaErr, secrets.ErrSecretNotFound) {
		return metaErr
	}
	if err := store.Set(contextName, name, value, secrets.KindEnv); err != nil {
		return err
	}

	log.Infof("env var %q set in context %q", name, contextName)
	return nil
}
