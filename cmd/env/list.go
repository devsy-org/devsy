package env

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

type ListCmd struct {
	*flags.GlobalFlags
}

func NewListCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListCmd{GlobalFlags: flags}
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List environment variables in the active context",
		Args:    cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	return listCmd
}

type envEntry struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Context  string `json:"context"`
	Attached bool   `json:"attached"`
}

func (cmd *ListCmd) Run(_ context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	contextName := devsyConfig.DefaultContext
	store, err := secrets.NewStoreForConfig(devsyConfig)
	if err != nil {
		return err
	}
	metas, err := store.List(contextName)
	if err != nil {
		return err
	}

	entries := make([]envEntry, 0, len(metas))
	for _, m := range metas {
		if m.Sensitive() {
			continue
		}
		entries = append(
			entries,
			envEntry{
				Name:     m.Name,
				Value:    m.Value,
				Context:  contextName,
				Attached: slices.Contains(devsyConfig.Contexts[contextName].EnvVars, m.Name),
			},
		)
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	if mode == output.ModeJSON {
		return renderJSON(entries)
	}
	renderPlain(entries)
	return nil
}

func renderJSON(entries []envEntry) error {
	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	//nolint:forbidigo // list --result-format json prints structured data to stdout.
	fmt.Print(string(out))
	return nil
}

func renderPlain(entries []envEntry) {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{e.Name, e.Value, e.Context, fmt.Sprint(e.Attached)})
	}
	table.Print([]string{"Name", "Value", "Context", "Attached"}, rows)
}
