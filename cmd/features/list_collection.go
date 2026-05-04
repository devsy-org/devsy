package features

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

type ListCollectionCmd struct {
	*flags.GlobalFlags

	Output string
}

func NewListCollectionCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListCollectionCmd{
		GlobalFlags: globalFlags,
	}
	listCollectionCmd := &cobra.Command{
		Use:   "list-collection <registry/namespace>",
		Short: "List features in a devcontainer feature collection",
		Long: `Lists all features available in a devcontainer feature collection
published as an OCI artifact. The argument should be in the format
"registry/namespace" (e.g., "ghcr.io/devcontainers/features").`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.Run(args[0])
		},
	}

	listCollectionCmd.Flags().
		StringVar(&cmd.Output, "output", "plain", "The output format to use. Can be json or plain")
	return listCollectionCmd
}

func (cmd *ListCollectionCmd) Run(ref string) error {
	registry, namespace, err := parseCollectionRef(ref)
	if err != nil {
		return err
	}

	features, err := feature.ListCollectionFeatures(registry, namespace)
	if err != nil {
		return fmt.Errorf("fetch collection: %w", err)
	}

	switch cmd.Output {
	case "json":
		return printFeaturesJSON(features)
	case "plain":
		printFeaturesTable(features)
	default:
		return fmt.Errorf(
			"unexpected output format, choose either json or plain. Got %s",
			cmd.Output,
		)
	}

	return nil
}

func printFeaturesJSON(features []feature.CollectionFeature) error {
	out, err := json.MarshalIndent(features, "", "  ")
	if err != nil {
		return err
	}
	_, _ = os.Stdout.WriteString(string(out) + "\n")
	return nil
}

func printFeaturesTable(features []feature.CollectionFeature) {
	rows := make([][]string, 0, len(features))
	for _, f := range features {
		deprecated := ""
		if f.Deprecated {
			deprecated = "yes"
		}
		rows = append(rows, []string{
			f.ID,
			f.Version,
			f.Name,
			f.Description,
			deprecated,
		})
	}
	table.Print([]string{"ID", "Version", "Name", "Description", "Deprecated"}, rows)
}

func parseCollectionRef(ref string) (string, string, error) {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf(
			"invalid collection reference %q: expected format \"registry/namespace\" "+
				"(e.g., \"ghcr.io/devcontainers/features\")",
			ref,
		)
	}
	return parts[0], parts[1], nil
}
