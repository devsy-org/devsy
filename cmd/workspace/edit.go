package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	workspace "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

type editPresentationFunc func(workspace.PresentationRequest) (*workspace.PresentationResult, error)

type EditCmd struct {
	*flags.GlobalFlags

	DisplayName          string
	SSHHostAlias         string
	ExpectedUID          string
	ExpectedDisplayName  string
	ExpectedSSHHostAlias string

	displayNameSet          bool
	sshHostAliasSet         bool
	expectedDisplayNameSet  bool
	expectedSSHHostAliasSet bool

	edit editPresentationFunc
}

func NewEditCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return newEditCmd(globalFlags, workspace.EditPresentation)
}

func newEditCmd(globalFlags *flags.GlobalFlags, edit editPresentationFunc) *cobra.Command {
	cmd := &EditCmd{
		GlobalFlags: globalFlags,
		edit:        edit,
	}

	editCmd := &cobra.Command{
		Use:   "edit <workspace> [--display-name <label>] [--ssh-host-alias <alias>]",
		Short: "Edit a workspace's display label and preferred SSH host alias",
		Long: `Edit how a workspace is presented without changing its ID.

--display-name sets a local human-facing label. --ssh-host-alias sets an
additional SSH host name (without the .devsy suffix) that resolves to the
workspace; the canonical <id>.devsy host keeps working. Pass an empty value to
clear either override. Label edits never touch the SSH config.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cmd.displayNameSet = cobraCmd.Flags().Changed(names.DisplayName)
			cmd.sshHostAliasSet = cobraCmd.Flags().Changed(names.SSHHostAlias)
			cmd.expectedDisplayNameSet = cobraCmd.Flags().Changed(names.ExpectedDisplayName)
			cmd.expectedSSHHostAliasSet = cobraCmd.Flags().Changed(names.ExpectedSSHHostAlias)
			return cmd.Run(cobraCmd.OutOrStdout(), args[0])
		},
		ValidArgsFunction: func(
			rootCmd *cobra.Command, args []string, toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completion.GetWorkspaceSuggestions(
					rootCmd,
					completion.SuggestionRequest{
						Context:    cmd.Context,
						Provider:   cmd.Provider,
						Args:       args,
						ToComplete: toComplete,
						Owner:      cmd.Owner,
					},
				)
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
	}

	cliflags.Add(
		editCmd,
		cliflags.String(&cmd.DisplayName, names.DisplayName, "",
			"Local display label for the workspace (empty clears the label)"),
		cliflags.String(&cmd.SSHHostAlias, names.SSHHostAlias, "",
			"Preferred SSH host alias without the .devsy suffix (empty clears the alias)"),
		cliflags.String(&cmd.ExpectedUID, names.ExpectedUID, "",
			"Fail unless the workspace still has this UID").Hidden(),
		cliflags.String(&cmd.ExpectedDisplayName, names.ExpectedDisplayName, "",
			"Fail unless the current display label matches").Hidden(),
		cliflags.String(&cmd.ExpectedSSHHostAlias, names.ExpectedSSHHostAlias, "",
			"Fail unless the current SSH host alias matches").Hidden(),
	)
	return editCmd
}

func (cmd *EditCmd) Run(out io.Writer, id string) error {
	if !cmd.displayNameSet && !cmd.sshHostAliasSet {
		return fmt.Errorf(
			"at least one of --%s or --%s is required",
			names.DisplayName,
			names.SSHHostAlias,
		)
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	result, err := cmd.edit(cmd.request(devsyConfig, id))
	if err != nil {
		return presentationCLIError(err)
	}

	return renderEdit(out, mode, result)
}

func (cmd *EditCmd) request(
	devsyConfig *config.Config,
	id string,
) workspace.PresentationRequest {
	req := workspace.PresentationRequest{
		DevsyConfig: devsyConfig,
		ID:          id,
		ExpectedUID: cmd.ExpectedUID,
	}
	if cmd.displayNameSet {
		req.DisplayName = &cmd.DisplayName
	}
	if cmd.sshHostAliasSet {
		req.SSHHostAlias = &cmd.SSHHostAlias
	}
	if cmd.expectedDisplayNameSet {
		req.ExpectedDisplayName = &cmd.ExpectedDisplayName
	}
	if cmd.expectedSSHHostAliasSet {
		req.ExpectedSSHHostAlias = &cmd.ExpectedSSHHostAlias
	}
	return req
}

func presentationCLIError(err error) error {
	presentationErr, ok := errors.AsType[*workspace.PresentationError](err)
	if !ok {
		return err
	}

	var errContext map[string]string
	if presentationErr.Field != "" || presentationErr.Conflict != "" {
		errContext = map[string]string{}
	}
	if presentationErr.Field != "" {
		errContext["field"] = presentationErr.Field
	}
	if presentationErr.Conflict != "" {
		errContext["conflict"] = presentationErr.Conflict
	}

	message := presentationErr.Message
	if message == "" {
		message = presentationErr.Error()
	}
	if joined := err.Error(); joined != presentationErr.Error() {
		message = joined
	}

	return &clierr.CLIError{
		Code:    clierr.Code(presentationErr.Code),
		Message: message,
		Context: errContext,
	}
}

func renderEdit(out io.Writer, mode string, result *workspace.PresentationResult) error {
	switch mode {
	case output.ModeJSON:
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(out, string(encoded))
		return err
	case output.ModePlain:
		return writeEditRows(out, result)
	}

	return nil
}

func writeEditRows(out io.Writer, result *workspace.PresentationResult) error {
	rows := [][]string{
		{"ID", result.ID},
		{"Label", result.EffectiveDisplayName},
		{"SSH Host", result.PreferredSSHHost},
	}
	if result.CanonicalSSHHost != result.PreferredSSHHost {
		rows = append(rows, []string{"Canonical SSH Host", result.CanonicalSSHHost})
	}
	_, err := fmt.Fprintln(out, table.Render([]string{"Field", "Value"}, rows))
	return err
}
