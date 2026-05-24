package features

import (
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/spf13/cobra"
)

type InfoTagsCmd struct {
	*flags.GlobalFlags

	Output string
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

	tagsCmd.Flags().StringVar(&cmd.Output, "output", "text", "Output format (text or json)")

	return tagsCmd
}

func (cmd *InfoTagsCmd) Run(featureID string) error {
	if err := validateOutputFormat(cmd.Output); err != nil {
		return err
	}

	ref, err := name.ParseReference(featureID)
	if err != nil {
		return fmt.Errorf("invalid feature reference %q: %w", featureID, err)
	}

	tags, err := listTags(ref)
	if err != nil {
		return fmt.Errorf("list tags: %w", err)
	}

	if cmd.Output == outputJSON {
		return writeJSON(os.Stdout, &tagsOutput{Tags: tags})
	}

	if len(tags) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No tags found.")
		return nil
	}

	rows := make([][]string, 0, len(tags))
	for _, tag := range tags {
		rows = append(rows, []string{tag})
	}
	table.Print([]string{"Tag"}, rows)
	return nil
}
