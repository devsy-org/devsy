package features

import (
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
)

type InfoCmd struct {
	*flags.GlobalFlags

	Output           string
	ShowTags         bool
	ShowDependencies bool
	Verbose          bool
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

	infoCmd.Flags().StringVar(&cmd.Output, "output", "text", "Output format (text or json)")
	infoCmd.Flags().BoolVar(
		&cmd.ShowTags, "show-tags", false, "List available tags from the registry",
	)
	infoCmd.Flags().BoolVar(
		&cmd.ShowDependencies, "show-dependencies", false, "Show declared dependencies",
	)
	infoCmd.Flags().BoolVar(
		&cmd.Verbose, "verbose", false, "Show full manifest and config details",
	)

	return infoCmd
}

func (cmd *InfoCmd) Run(featureID string) error {
	if err := validateOutputFormat(cmd.Output); err != nil {
		return err
	}

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

	if cmd.Output == outputJSON {
		return writeJSON(os.Stdout, info)
	}
	return cmd.printText(info)
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

	if cmd.Verbose {
		info.Options = featureCfg.Options
	}

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
	_, _ = fmt.Fprintf(w, "Feature: %s\n", info.Name)
	printField(w, "ID", info.ID)
	printField(w, "Version", info.Version)
	printField(w, "Description", info.Description)
	printField(w, "Authors", info.Authors)
	printField(w, "Source", info.Source)
	printField(w, "Documentation", info.DocumentationURL)
	if info.Deprecated {
		_, _ = fmt.Fprintln(w, "Status: DEPRECATED")
	}

	cmd.printDependencies(w, info)
	cmd.printTags(w, info)
	cmd.printOptions(w, info)
	cmd.printAnnotations(w, info)

	return nil
}

func printField(w *os.File, label, value string) {
	if value != "" {
		_, _ = fmt.Fprintf(w, "%s: %s\n", label, value)
	}
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
	_, _ = fmt.Fprintf(w, "  %s\n", strings.Join(info.Tags, ", "))
}

func (cmd *InfoCmd) printOptions(w *os.File, info *featureInfo) {
	if !cmd.Verbose || len(info.Options) == 0 {
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
	if !cmd.Verbose || len(info.Annotations) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nOCI Annotations:")
	headers := []string{"Key", "Value"}
	var rows [][]string
	for k, v := range info.Annotations {
		rows = append(rows, []string{k, v})
	}
	table.Print(headers, rows)
}
