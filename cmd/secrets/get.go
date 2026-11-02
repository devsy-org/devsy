package secrets

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

type GetCmd struct {
	*flags.GlobalFlags
}

func NewGetCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &GetCmd{
		GlobalFlags: flags,
	}
	getCmd := &cobra.Command{
		Use:   "get NAME",
		Short: "Print a secret's value",
		Long: `Print a secret's value to standard output.

Use it in scripts. The value is followed by a single newline.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	return getCmd
}

func (cmd *GetCmd) Run(_ context.Context, name string) error {
	contextName, store, err := resolveContext(cmd.GlobalFlags)
	if err != nil {
		return err
	}

	meta, err := store.Meta(contextName, name)
	if err != nil {
		return err
	}
	if !meta.Sensitive() {
		return fmt.Errorf("%q is an environment variable; use \"devsy env get\"", name)
	}

	value, err := store.Get(contextName, name)
	if err != nil {
		return err
	}

	//nolint:forbidigo // get prints the raw value to stdout for scripting.
	fmt.Println(value)
	return nil
}
