package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/spf13/cobra"
)

const templateFlag = "template"

// CreateCmd holds the configuration of `devsy workspace create`.
type CreateCmd struct {
	*flags.GlobalFlags

	Template string

	// Out receives the result. Nil selects stdout.
	Out io.Writer
}

// NewCreateCmd creates the `devsy workspace create` command, which asks the
// platform gateway for a new workspace.
func NewCreateCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &CreateCmd{GlobalFlags: globalFlags}
	createCmd := &cobra.Command{
		Use:   "create NAME --template TEMPLATE",
		Short: "Create a workspace on the platform gateway",
		Long: "Create a workspace on the platform gateway you are logged in to.\n\n" +
			"The gateway accepts the request and the workspace is built in the " +
			"background, so it is not ready when this command returns. " +
			"Run 'devsy workspace list' to follow its phase.",
		Example: "  devsy workspace create my-workspace --template go-dev",
		Args:    cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cmd.Out = cobraCmd.OutOrStdout()
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}
	createCmd.Flags().
		StringVar(&cmd.Template, templateFlag, "", "Template to create the workspace from")
	_ = createCmd.MarkFlagRequired(templateFlag)
	return createCmd
}

// Run sends the create request and returns once the gateway has accepted it.
func (cmd *CreateCmd) Run(ctx context.Context, name string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	creds := platformCredentials(devsyConfig)
	if creds == nil {
		return errors.New(
			"not logged in to a platform gateway: run 'devsy login <gateway-url>' first",
		)
	}
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}

	created, err := newPlatformClient(creds).CreateWorkspace(ctx, creds.APIKey, name, cmd.Template)
	if err != nil {
		return fmt.Errorf("creating workspace %q: %w", name, err)
	}

	out := cmd.Out
	if out == nil {
		out = os.Stdout
	}
	if mode == output.ModeJSON {
		return writePlatformJSON(out, created)
	}
	_, err = fmt.Fprintf(out, "Workspace %s accepted (phase: %s)\n", created.Name, created.Phase)
	return err
}
