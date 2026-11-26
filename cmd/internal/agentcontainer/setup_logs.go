package agentcontainer

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/spf13/cobra"
)

// resolveSpool is replaced in tests.
var resolveSpool = workspacelogs.ResolveSpool

// SetupLogsCmd reads one bounded page of the container spool.
type SetupLogsCmd struct {
	*flags.GlobalFlags

	Run        string
	Hook       string
	Invocation string
	After      string
	Limit      int
}

// NewSetupLogsCmd creates the hidden read-only spool reader. It never creates
// the spool, installs anything or runs a hook.
func NewSetupLogsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetupLogsCmd{GlobalFlags: globalFlags}
	c := &cobra.Command{
		Use:    "setup-logs",
		Short:  "Print one page of retained setup output from the container spool",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return cmd.run(c)
		},
	}
	c.Flags().StringVar(&cmd.Run, "run", "", "The run ID")
	c.Flags().StringVar(&cmd.Hook, "hook", "", "Only this lifecycle hook")
	c.Flags().StringVar(&cmd.Invocation, "invocation", "", "Only this invocation ID")
	c.Flags().StringVar(&cmd.After, "after", "", "Cursor of the previous page")
	c.Flags().IntVar(&cmd.Limit, "limit", 0, "Most records in the page")
	_ = c.MarkFlagRequired("run")

	return c
}

func (cmd *SetupLogsCmd) run(c *cobra.Command) error {
	read := workspacelogs.ReadRequest{
		RunID:        cmd.Run,
		Hook:         cmd.Hook,
		InvocationID: cmd.Invocation,
		After:        cmd.After,
		Limit:        cmd.Limit,
	}
	if err := workspacelogs.ValidateReadSelectors(read); err != nil {
		return err
	}
	root, err := resolveSpool()
	if errors.Is(err, workspacelogs.ErrNotFound) {
		return unavailable(c, workspacelogs.ReasonNoSpool, err)
	}
	if err != nil {
		return err
	}
	resp, err := workspacelogs.ReadSpoolPage(workspacelogs.SpoolSelector{
		Root:         root,
		Context:      cmd.Context,
		WorkspaceUID: cmd.UID,
		Read:         read,
	})
	if errors.Is(err, workspacelogs.ErrNotFound) {
		return unavailable(c, workspacelogs.ReasonNoSpool, err)
	}
	if err != nil {
		return err
	}

	return json.NewEncoder(c.OutOrStdout()).Encode(resp)
}

// unavailable prints the typed reason on stderr so the caller can tell absent
// history from a command error.
func unavailable(c *cobra.Command, reason string, cause error) error {
	_, _ = fmt.Fprint(c.ErrOrStderr(), workspacelogs.FormatUnavailable(reason))

	return cause
}
