package env

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
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
	contextName, store, err := resolveContext(cmd.GlobalFlags)
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
	if err := store.Delete(contextName, name); err != nil {
		return err
	}
	log.Infof("env var %q deleted from context %q", name, contextName)
	return nil
}
