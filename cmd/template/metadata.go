package template

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/spf13/cobra"
)

type MetadataFlags struct {
	TemplateID string
}

func NewMetadataCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	metadataFlags := &MetadataFlags{}
	metadataCmd := &cobra.Command{
		Use:   "metadata",
		Short: "Fetch published template metadata from OCI registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMetadata(metadataFlags)
		},
	}

	metadataCmd.Flags().StringVar(
		&metadataFlags.TemplateID, "template-id", "",
		"OCI reference of the template",
	)
	_ = metadataCmd.MarkFlagRequired("template-id")

	return metadataCmd
}

func runMetadata(f *MetadataFlags) error {
	ref, err := name.ParseReference(f.TemplateID)
	if err != nil {
		return fmt.Errorf("parse template reference: %w", err)
	}

	log.Infof("Fetching metadata for: %s", ref.String())

	img, err := pullTemplateImage(ref)
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp("", "devsy-template-metadata-*")
	if err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	if err := extractTemplateToDir(img, tempDir); err != nil {
		return err
	}

	metadata, err := readTemplateMetadata(tempDir)
	if err != nil {
		return err
	}

	output, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	_, _ = os.Stdout.Write(output)
	_, _ = os.Stdout.WriteString("\n")

	return nil
}
