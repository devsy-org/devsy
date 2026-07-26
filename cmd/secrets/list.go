package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
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
	Created  string `json:"created,omitempty"`
	LastUsed string `json:"lastUsed,omitempty"`
	Orphaned bool   `json:"orphaned,omitempty"`
}

func (cmd *ListCmd) Run(_ context.Context) error {
	contextName, store, err := resolveContext(cmd.GlobalFlags)
	if err != nil {
		return err
	}

	all, err := store.List(contextName)
	if err != nil {
		return err
	}
	metas := make([]secrets.SecretMeta, 0, len(all))
	for _, m := range all {
		if m.Sensitive() {
			metas = append(metas, m)
		}
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}

	switch mode {
	case output.ModePlain:
		renderPlain(metas)
	case output.ModeJSON:
		return renderJSON(metas)
	}

	return nil
}

func renderPlain(metas []secrets.SecretMeta) {
	tableEntries := [][]string{}
	for _, m := range metas {
		tableEntries = append(tableEntries, []string{
			m.Name,
			formatTime(m.Created),
			formatTime(m.LastUsed),
			orphanLabel(m.Orphaned),
		})
	}
	table.Print([]string{"Name", "Created", "Last Used", "Status"}, tableEntries)
}

func renderJSON(metas []secrets.SecretMeta) error {
	entries := []secretEntry{}
	for _, m := range metas {
		entries = append(entries, secretEntry{
			Name:     m.Name,
			Context:  m.Context,
			Created:  formatTime(m.Created),
			LastUsed: formatTime(m.LastUsed),
			Orphaned: m.Orphaned,
		})
	}
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
		return "orphaned"
	}
	return "ok"
}
