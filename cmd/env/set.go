package env

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
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
	name, value, err := parseEnvAssignment(arg, cmd.Value, cmd.valueSet)
	if err != nil {
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
	if attachedLocalSecret(ctxConfig, name) {
		return fmt.Errorf(
			"%q is attached as a secret; detach it before converting it to an environment variable",
			name,
		)
	}
	store, err := envstore.NewStoreForConfig(devsyConfig)
	if err != nil {
		return err
	}
	if err := store.Set(contextName, name, value); err != nil {
		return err
	}

	log.Infof("env var %q set in context %q", name, contextName)
	return nil
}

func parseEnvAssignment(arg, value string, valueSet bool) (string, string, error) {
	name := arg
	if k, v, ok := strings.Cut(arg, "="); ok {
		if valueSet {
			return "", "", fmt.Errorf(
				"specify the value inline as NAME=VALUE or with --value, not both",
			)
		}
		name, value = k, v
	}
	if err := envstore.ValidateName(name); err != nil {
		return "", "", err
	}
	return name, value, nil
}
