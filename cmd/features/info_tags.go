package features

import (
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/spf13/cobra"
)

type InfoTagsCmd struct {
	*flags.GlobalFlags
}

type tagsOutput struct {
	Tags []string `json:"tags"`
}

func NewInfoTagsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &InfoTagsCmd{GlobalFlags: globalFlags}
	tagsCmd := &cobra.Command{
		Use:   "tags <feature-id>",
		Short: "List available tags for a published feature",
		Long: `List all available tags from the registry for a published dev container feature.

Accepts a feature ID like ghcr.io/devcontainers/features/go and
queries the registry for all available image tags.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.Run(args[0])
		},
	}

	return tagsCmd
}

func (cmd *InfoTagsCmd) Run(featureID string) error {
	ref, err := name.ParseReference(featureID)
	if err != nil {
		return fmt.Errorf("invalid feature reference %q: %w", featureID, err)
	}

	tags, err := listTags(ref)
	if err != nil {
		return fmt.Errorf("list tags: %w", err)
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModeJSON:
		return writeJSON(os.Stdout, &tagsOutput{Tags: tags})
	case output.ModePlain:
		if len(tags) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "No tags found.")
			return nil
		}

		_, _ = fmt.Fprintln(os.Stdout, "Available Tags:")
		rows := make([][]string, 0, len(tags))
		for _, tag := range tags {
			rows = append(rows, []string{tag})
		}
		table.Print([]string{"Tag"}, rows)
		return nil
	}
	return nil
}
