package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

type GenerateDocsFlags struct {
	ProjectFolder string
	GithubOwner   string
	GithubRepo    string
}

func NewGenerateDocsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	docsFlags := &GenerateDocsFlags{}
	docsCmd := &cobra.Command{
		Use:   "generate-docs",
		Short: "Generate markdown docs from template metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenerateDocs(docsFlags)
		},
	}

	docsCmd.Flags().StringVar(
		&docsFlags.ProjectFolder, "project-folder", "",
		"Path to project folder containing template sources",
	)
	docsCmd.Flags().StringVar(
		&docsFlags.GithubOwner, "github-owner", "",
		"GitHub owner for documentation links",
	)
	docsCmd.Flags().StringVar(
		&docsFlags.GithubRepo, "github-repo", "",
		"GitHub repo for documentation links",
	)
	_ = docsCmd.MarkFlagRequired("project-folder")

	return docsCmd
}

func runGenerateDocs(f *GenerateDocsFlags) error {
	projectFolder, err := filepath.Abs(f.ProjectFolder)
	if err != nil {
		return fmt.Errorf("resolve project folder: %w", err)
	}

	templates, err := discoverTemplates(projectFolder)
	if err != nil {
		return err
	}

	if len(templates) == 0 {
		log.Infof("No templates found in %s", projectFolder)
		return nil
	}

	for _, tmpl := range templates {
		docPath := filepath.Join(filepath.Dir(tmpl.path), "README.md")
		content := generateTemplateDoc(tmpl.metadata, f.GithubOwner, f.GithubRepo)

		// #nosec G306 -- documentation files need to be readable
		if err := os.WriteFile(docPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write documentation for %s: %w", tmpl.metadata.ID, err)
		}

		log.Infof("Generated docs: %s", docPath)
	}

	log.Infof("Generated documentation for %d template(s)", len(templates))

	return nil
}

type discoveredTemplate struct {
	path     string
	metadata *TemplateMetadata
}

func discoverTemplates(projectFolder string) ([]discoveredTemplate, error) {
	var templates []discoveredTemplate

	err := filepath.Walk(projectFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.Name() != templateMetadataFile {
			return nil
		}

		//nolint:gosec // path from filepath.Walk within project folder
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return err
		}

		var metadata TemplateMetadata
		if err := json.Unmarshal(data, &metadata); err != nil {
			log.Debugf("skipping invalid template metadata: %s", path)
			return nil
		}

		templates = append(templates, discoveredTemplate{
			path:     path,
			metadata: &metadata,
		})

		return nil
	})

	return templates, err
}

func generateTemplateDoc(
	metadata *TemplateMetadata,
	githubOwner, githubRepo string,
) string {
	var sb strings.Builder

	title := metadata.Name
	if title == "" {
		title = metadata.ID
	}

	fmt.Fprintf(&sb, "# %s\n\n", title)

	if metadata.Description != "" {
		fmt.Fprintf(&sb, "%s\n\n", metadata.Description)
	}

	if metadata.Version != "" {
		fmt.Fprintf(&sb, "**Version:** %s\n\n", metadata.Version)
	}

	if githubOwner != "" && githubRepo != "" {
		fmt.Fprintf(
			&sb,
			"**Source:** [%s/%s](https://github.com/%s/%s)\n\n",
			githubOwner, githubRepo, githubOwner, githubRepo,
		)
	}

	writeOptionsTable(&sb, metadata)

	return sb.String()
}

func writeOptionsTable(sb *strings.Builder, metadata *TemplateMetadata) {
	if len(metadata.Options) == 0 {
		return
	}

	sb.WriteString("## Options\n\n")
	sb.WriteString("| Option | Type | Default | Description |\n")
	sb.WriteString("|--------|------|---------|-------------|\n")

	for key, opt := range metadata.Options {
		defaultVal := ""
		if opt.Default != nil {
			defaultVal = fmt.Sprintf("%v", opt.Default)
		}

		fmt.Fprintf(sb, "| %s | %s | %s | %s |\n", key, opt.Type, defaultVal, opt.Description)
	}

	sb.WriteString("\n")
}
