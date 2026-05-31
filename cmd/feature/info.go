package feature

import (
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
)

type InfoCmd struct {
	*flags.GlobalFlags

	ShowTags         bool
	ShowDependencies bool
}

type featureInfo struct {
	ID               string                                `json:"id"`
	Name             string                                `json:"name,omitempty"`
	Version          string                                `json:"version,omitempty"`
	Description      string                                `json:"description,omitempty"`
	Authors          string                                `json:"authors,omitempty"`
	Source           string                                `json:"source,omitempty"`
	DocumentationURL string                                `json:"documentationURL,omitempty"`
	Deprecated       bool                                  `json:"deprecated,omitempty"`
	Tags             []string                              `json:"tags,omitempty"`
	Dependencies     map[string]any                        `json:"dependencies,omitempty"`
	Options          map[string]config.FeatureConfigOption `json:"options,omitempty"`
	Annotations      map[string]string                     `json:"annotations,omitempty"`
}

func NewInfoCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &InfoCmd{GlobalFlags: globalFlags}
	infoCmd := &cobra.Command{
		Use:   "info <feature-id>",
		Short: "Fetch and display OCI metadata for a published feature",
		Long: `Fetch and display OCI metadata for a published dev container feature.

Accepts a feature ID like ghcr.io/devcontainers/features/go or
ghcr.io/devcontainers/features/go:1 and pulls its OCI manifest
to display metadata.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.Run(args[0])
		},
	}

	infoCmd.Flags().BoolVar(
		&cmd.ShowTags, "show-tags", false, "List available tags from the registry",
	)
	infoCmd.Flags().BoolVar(
		&cmd.ShowDependencies, "show-dependencies", false, "Show declared dependencies",
	)

	infoCmd.AddCommand(NewInfoManifestCmd(globalFlags))
	infoCmd.AddCommand(NewInfoTagsCmd(globalFlags))

	return infoCmd
}

func (cmd *InfoCmd) Run(featureID string) error {
	ref, err := name.ParseReference(featureID)
	if err != nil {
		return fmt.Errorf("invalid feature reference %q: %w", featureID, err)
	}

	info, err := cmd.fetchInfo(ref, featureID)
	if err != nil {
		return err
	}

	if cmd.ShowTags {
		tags, tagErr := listTags(ref)
		if tagErr != nil {
			return fmt.Errorf("list tags: %w", tagErr)
		}
		info.Tags = tags
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModeJSON:
		return writeJSON(os.Stdout, info)
	case output.ModePlain:
		return cmd.printText(info)
	}
	return nil
}

func (cmd *InfoCmd) fetchInfo(
	ref name.Reference, featureID string,
) (*featureInfo, error) {
	folder, err := feature.PullFeatureToTemp(ref, featureID)
	if err != nil {
		return nil, fmt.Errorf("pull feature: %w", err)
	}

	featureCfg, err := config.ParseDevContainerFeature(folder)
	if err != nil {
		return nil, fmt.Errorf("parse feature config: %w", err)
	}

	annotations := feature.LoadOCIAnnotations(folder)

	info := &featureInfo{
		ID:               featureCfg.ID,
		Name:             featureCfg.Name,
		Version:          featureCfg.Version,
		Description:      featureCfg.Description,
		DocumentationURL: featureCfg.DocumentationURL,
		Deprecated:       featureCfg.Deprecated,
		Annotations:      annotations,
	}

	if annotations != nil {
		info.Authors = annotations["org.opencontainers.image.authors"]
		info.Source = annotations["org.opencontainers.image.source"]
	}

	if cmd.ShowDependencies && featureCfg.DependsOn != nil {
		info.Dependencies = featureCfg.DependsOn
	}

	info.Options = featureCfg.Options

	return info, nil
}

func listTags(ref name.Reference) ([]string, error) {
	repo := ref.Context()
	tags, err := remote.List(repo, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return nil, err
	}
	return tags, nil
}

func (cmd *InfoCmd) printText(info *featureInfo) error {
	w := os.Stdout

	rows := [][]string{{headerFeature, info.Name}}
	rows = appendField(rows, "ID", info.ID)
	rows = appendField(rows, "Version", info.Version)
	rows = appendField(rows, "Description", info.Description)
	rows = appendField(rows, "Authors", info.Authors)
	rows = appendField(rows, "Source", info.Source)
	rows = appendField(rows, "Documentation", info.DocumentationURL)
	if info.Deprecated {
		rows = append(rows, []string{"Status", "DEPRECATED"})
	}
	table.Print([]string{"Property", headerValue}, rows)

	cmd.printDependencies(w, info)
	cmd.printTags(w, info)
	cmd.printOptions(w, info)
	cmd.printAnnotations(w, info)

	return nil
}

func appendField(rows [][]string, label, value string) [][]string {
	if value != "" {
		return append(rows, []string{label, value})
	}
	return rows
}

func (cmd *InfoCmd) printDependencies(w *os.File, info *featureInfo) {
	if !cmd.ShowDependencies || len(info.Dependencies) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nDependencies:")
	headers := []string{"Dependency"}
	var rows [][]string
	for dep := range info.Dependencies {
		rows = append(rows, []string{dep})
	}
	table.Print(headers, rows)
}

func (cmd *InfoCmd) printTags(w *os.File, info *featureInfo) {
	if !cmd.ShowTags || len(info.Tags) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nAvailable Tags:")
	rows := make([][]string, 0, len(info.Tags))
	for _, tag := range info.Tags {
		rows = append(rows, []string{tag})
	}
	table.Print([]string{"Tag"}, rows)
}

func (cmd *InfoCmd) printOptions(w *os.File, info *featureInfo) {
	if len(info.Options) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nOptions:")
	headers := []string{"Name", "Type", "Description", "Default"}
	var rows [][]string
	for optName, opt := range info.Options {
		rows = append(rows, []string{optName, opt.Type, opt.Description, string(opt.Default)})
	}
	table.Print(headers, rows)
}

func (cmd *InfoCmd) printAnnotations(w *os.File, info *featureInfo) {
	if len(info.Annotations) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nOCI Annotations:")
	headers := []string{"Key", headerValue}
	var rows [][]string
	for k, v := range info.Annotations {
		rows = append(rows, []string{k, v})
	}
	table.Print(headers, rows)
}
