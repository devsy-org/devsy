package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

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
	cmd := &ListCmd{
		GlobalFlags: flags,
	}
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List secrets in the active context",
		Args:    cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	return listCmd
}

type secretEntry struct {
	Name     string `json:"name"`
	Context  string `json:"context"`
	Backend  string `json:"backend,omitempty"`
	Created  string `json:"created,omitempty"`
	LastUsed string `json:"lastUsed,omitempty"`
	Orphaned bool   `json:"orphaned,omitempty"`
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
	entries, err := listEntries(devsyConfig, store, contextName)
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}

	switch mode {
	case output.ModePlain:
		renderPlain(entries)
	case output.ModeJSON:
		return renderJSON(entries)
	}

	return nil
}

func listEntries(
	devsyConfig *config.Config,
	store secrets.Store,
	contextName string,
) ([]secretEntry, error) {
	all, err := store.List(contextName)
	if err != nil {
		return nil, err
	}
	var attachedNames []string
	if ctxConfig := devsyConfig.Contexts[contextName]; ctxConfig != nil {
		attachedNames = ctxConfig.Secrets
	}
	entries := make([]secretEntry, 0, len(all))
	for _, m := range all {
		if !m.Sensitive() {
			continue
		}
		entries = append(entries, secretEntry{
			Name:     m.Name,
			Context:  m.Context,
			Backend:  string(m.Backend),
			Created:  formatTime(m.Created),
			LastUsed: formatTime(m.LastUsed),
			Orphaned: m.Orphaned,
			Attached: slices.Contains(attachedNames, m.Name),
		})
	}
	return entries, nil
}

func renderPlain(entries []secretEntry) {
	tableEntries := [][]string{}
	for _, e := range entries {
		tableEntries = append(tableEntries, []string{
			e.Name,
			e.Created,
			e.LastUsed,
			orphanLabel(e.Orphaned),
			fmt.Sprint(e.Attached),
		})
	}
	table.Print([]string{"Name", "Created", "Last Used", "Status", "Attached"}, tableEntries)
}

func renderJSON(entries []secretEntry) error {
	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}

	//nolint:forbidigo // list --result-format json prints structured data to stdout.
	fmt.Print(string(out))
	return nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func orphanLabel(orphaned bool) string {
	if orphaned {
		return "missing value"
	}
	return "ok"
}
