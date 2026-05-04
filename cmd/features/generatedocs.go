package features

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/spf13/cobra"
)

type GenerateDocsCmd struct {
	*flags.GlobalFlags

	ProjectFolder string
	OutputFolder  string
	Namespace     string
	Output        string
}

func NewGenerateDocsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &GenerateDocsCmd{GlobalFlags: globalFlags}
	generateDocsCmd := &cobra.Command{
		Use:   "generate-docs",
		Short: "Generate markdown documentation from feature metadata",
		Long: `Scan a feature project's src/ directory and generate markdown
documentation for each feature based on its devcontainer-feature.json.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return cmd.Run()
		},
	}

	generateDocsCmd.Flags().StringVar(
		&cmd.ProjectFolder, "project-folder", "", "Path to feature project containing src/ directory",
	)
	generateDocsCmd.Flags().StringVar(
		&cmd.OutputFolder, "output-folder", "", "Where to write generated docs (default: project-folder)",
	)
	generateDocsCmd.Flags().StringVar(
		&cmd.Namespace, "namespace", "", "Registry namespace for linking (e.g. ghcr.io/myorg/features)",
	)
	generateDocsCmd.Flags().StringVar(
		&cmd.Output, "output", "text", "Output format (text or json)",
	)
	_ = generateDocsCmd.MarkFlagRequired("project-folder")

	return generateDocsCmd
}

func (cmd *GenerateDocsCmd) Run() error {
	if err := validateOutputFormat(cmd.Output); err != nil {
		return err
	}

	projectFolder, err := filepath.Abs(cmd.ProjectFolder)
	if err != nil {
		return fmt.Errorf("resolve project folder: %w", err)
	}

	outputFolder, err := cmd.resolveOutputFolder(projectFolder)
	if err != nil {
		return err
	}

	features, err := scanFeatures(filepath.Join(projectFolder, "src"))
	if err != nil {
		return err
	}

	if cmd.Output == outputJSON {
		return cmd.writeJSON(features)
	}

	return cmd.writeDocs(features, outputFolder)
}

func (cmd *GenerateDocsCmd) resolveOutputFolder(
	projectFolder string,
) (string, error) {
	if cmd.OutputFolder == "" {
		return projectFolder, nil
	}
	out, err := filepath.Abs(cmd.OutputFolder)
	if err != nil {
		return "", fmt.Errorf("resolve output folder: %w", err)
	}
	return out, nil
}

func scanFeatures(srcDir string) ([]*featureDoc, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, fmt.Errorf("read src/ directory: %w", err)
	}

	var features []*featureDoc
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		featureDir := filepath.Join(srcDir, entry.Name())
		featureCfg, parseErr := config.ParseDevContainerFeature(featureDir)
		if parseErr != nil {
			continue
		}

		features = append(features, &featureDoc{
			dir:    entry.Name(),
			config: featureCfg,
		})
	}

	if len(features) == 0 {
		return nil, fmt.Errorf("no features found in %s", srcDir)
	}
	return features, nil
}

func (cmd *GenerateDocsCmd) writeDocs(
	features []*featureDoc, outputFolder string,
) error {
	// #nosec G301
	if err := os.MkdirAll(outputFolder, 0o755); err != nil {
		return fmt.Errorf("create output folder: %w", err)
	}

	for _, f := range features {
		docPath := filepath.Join(outputFolder, f.dir+".md")
		content := cmd.generateFeatureDoc(f)
		// #nosec G306
		if err := os.WriteFile(docPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write doc for %s: %w", f.dir, err)
		}
		_, _ = fmt.Fprintf(os.Stdout, "Generated: %s\n", docPath)
	}

	indexPath := filepath.Join(outputFolder, "README.md")
	indexContent := cmd.generateIndex(features)
	// #nosec G306
	if err := os.WriteFile(indexPath, []byte(indexContent), 0o644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "Generated: %s\n", indexPath)

	return nil
}

type featureDoc struct {
	dir    string
	config *config.FeatureConfig
}

type featureDocJSON struct {
	ID          string                                `json:"id"`
	Name        string                                `json:"name,omitempty"`
	Version     string                                `json:"version,omitempty"`
	Description string                                `json:"description,omitempty"`
	Dir         string                                `json:"dir"`
	Namespace   string                                `json:"namespace,omitempty"`
	Options     map[string]config.FeatureConfigOption `json:"options,omitempty"`
}

func (cmd *GenerateDocsCmd) writeJSON(features []*featureDoc) error {
	docs := make([]featureDocJSON, 0, len(features))
	for _, f := range features {
		docs = append(docs, featureDocJSON{
			ID:          f.config.ID,
			Name:        f.config.Name,
			Version:     f.config.Version,
			Description: f.config.Description,
			Dir:         f.dir,
			Namespace:   cmd.Namespace,
			Options:     f.config.Options,
		})
	}
	data, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(data))
	return err
}

func (cmd *GenerateDocsCmd) generateFeatureDoc(f *featureDoc) string {
	var sb strings.Builder

	name := f.config.Name
	if name == "" {
		name = f.config.ID
	}

	sb.WriteString("# " + name + "\n\n")

	if f.config.Description != "" {
		sb.WriteString(f.config.Description + "\n\n")
	}

	cmd.writeMetadataTable(&sb, f)
	writeOptionsTable(&sb, f.config.Options)
	writeDependenciesSection(&sb, f.config)

	return sb.String()
}

func (cmd *GenerateDocsCmd) writeMetadataTable(
	sb *strings.Builder, f *featureDoc,
) {
	sb.WriteString("## Metadata\n\n")
	sb.WriteString("| Property | Value |\n")
	sb.WriteString("|----------|-------|\n")
	fmt.Fprintf(sb, "| ID | `%s` |\n", f.config.ID)
	if f.config.Version != "" {
		fmt.Fprintf(sb, "| Version | `%s` |\n", f.config.Version)
	}
	if cmd.Namespace != "" {
		fmt.Fprintf(sb, "| Registry | `%s/%s` |\n", cmd.Namespace, f.config.ID)
	}
	if f.config.DocumentationURL != "" {
		fmt.Fprintf(sb, "| Documentation | %s |\n", f.config.DocumentationURL)
	}
	if f.config.Deprecated {
		sb.WriteString("| Status | **DEPRECATED** |\n")
	}
	sb.WriteString("\n")
}

func writeOptionsTable(
	sb *strings.Builder, options map[string]config.FeatureConfigOption,
) {
	if len(options) == 0 {
		return
	}
	sb.WriteString("## Options\n\n")
	sb.WriteString("| Name | Type | Default | Description |\n")
	sb.WriteString("|------|------|---------|-------------|\n")
	for optName, opt := range options {
		defaultVal := string(opt.Default)
		if defaultVal == "" {
			defaultVal = "-"
		}
		desc := opt.Description
		if desc == "" {
			desc = "-"
		}
		optType := opt.Type
		if optType == "" {
			optType = "string"
		}
		fmt.Fprintf(sb, "| `%s` | %s | `%s` | %s |\n",
			optName, optType, defaultVal, desc)
	}
	sb.WriteString("\n")
}

func writeDependenciesSection(sb *strings.Builder, cfg *config.FeatureConfig) {
	if len(cfg.DependsOn) > 0 {
		sb.WriteString("## Dependencies\n\n")
		for dep := range cfg.DependsOn {
			fmt.Fprintf(sb, "- `%s`\n", dep)
		}
		sb.WriteString("\n")
	}

	if len(cfg.InstallsAfter) > 0 {
		sb.WriteString("## Install Order\n\n")
		sb.WriteString("This feature installs after:\n\n")
		for _, dep := range cfg.InstallsAfter {
			fmt.Fprintf(sb, "- `%s`\n", dep)
		}
		sb.WriteString("\n")
	}
}

func (cmd *GenerateDocsCmd) generateIndex(features []*featureDoc) string {
	var sb strings.Builder

	sb.WriteString("# Dev Container Features\n\n")

	if cmd.Namespace != "" {
		fmt.Fprintf(&sb, "Registry: `%s`\n\n", cmd.Namespace)
	}

	sb.WriteString("## Features\n\n")
	sb.WriteString("| Feature | Description |\n")
	sb.WriteString("|---------|-------------|\n")

	for _, f := range features {
		name := f.config.Name
		if name == "" {
			name = f.config.ID
		}
		desc := f.config.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(&sb, "| [%s](./%s.md) | %s |\n", name, f.dir, desc)
	}

	sb.WriteString("\n")
	return sb.String()
}
