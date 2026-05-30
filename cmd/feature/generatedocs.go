package feature

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

type GenerateDocsCmd struct {
	*flags.GlobalFlags

	ProjectFolder string
	OutputFolder  string
	Namespace     string
}

func NewGenerateDocsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &GenerateDocsCmd{GlobalFlags: globalFlags}
	generateDocsCmd := &cobra.Command{
		Use:   "docs",
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
	_ = generateDocsCmd.MarkFlagRequired("project-folder")

	return generateDocsCmd
}

func (cmd *GenerateDocsCmd) Run() error {
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
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

	switch mode {
	case output.ModeJSON:
		return cmd.writeJSON(features)
	case output.ModePlain:
		return cmd.writeDocs(features, outputFolder)
	}
	return nil
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
	rows := [][]string{{"ID", fmt.Sprintf("`%s`", f.config.ID)}}
	if f.config.Version != "" {
		rows = append(rows, []string{"Version", fmt.Sprintf("`%s`", f.config.Version)})
	}
	if cmd.Namespace != "" {
		rows = append(
			rows,
			[]string{"Registry", fmt.Sprintf("`%s/%s`", cmd.Namespace, f.config.ID)},
		)
	}
	if f.config.DocumentationURL != "" {
		rows = append(rows, []string{"Documentation", f.config.DocumentationURL})
	}
	if f.config.Deprecated {
		rows = append(rows, []string{"Status", "**DEPRECATED**"})
	}
	sb.WriteString("## Metadata\n\n")
	sb.WriteString(table.Markdown([]string{"Property", "Value"}, rows))
	sb.WriteString("\n")
}

func writeOptionsTable(
	sb *strings.Builder, options map[string]config.FeatureConfigOption,
) {
	if len(options) == 0 {
		return
	}
	rows := make([][]string, 0, len(options))
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
		rows = append(rows, []string{
			fmt.Sprintf("`%s`", optName),
			optType,
			fmt.Sprintf("`%s`", defaultVal),
			desc,
		})
	}
	sb.WriteString("## Options\n\n")
	sb.WriteString(table.Markdown([]string{"Name", "Type", "Default", "Description"}, rows))
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

	rows := make([][]string, 0, len(features))
	for _, f := range features {
		name := f.config.Name
		if name == "" {
			name = f.config.ID
		}
		desc := f.config.Description
		if desc == "" {
			desc = "-"
		}
		rows = append(rows, []string{
			fmt.Sprintf("[%s](./%s.md)", name, f.dir),
			desc,
		})
	}
	sb.WriteString("## Features\n\n")
	sb.WriteString(table.Markdown([]string{"Feature", "Description"}, rows))
	sb.WriteString("\n")
	return sb.String()
}
