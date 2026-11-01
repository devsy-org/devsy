package template

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

type PublishFlags struct {
	Target    string
	Registry  string
	Namespace string
}

func NewPublishCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	publishFlags := &PublishFlags{}
	publishCmd := &cobra.Command{
		Use:   "publish",
		Short: "Package and push templates to OCI registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPublish(publishFlags)
		},
	}

	cliflags.Add(
		publishCmd,
		cliflags.String(
			&publishFlags.Target,
			names.Target,
			"",
			"Path to template source directory",
		),
		cliflags.String(&publishFlags.Registry, names.Registry, "ghcr.io", "Target OCI registry"),
		cliflags.String(
			&publishFlags.Namespace,
			names.Namespace,
			"",
			"Registry namespace (e.g., devcontainers/templates)",
		),
	)
	_ = publishCmd.MarkFlagRequired(names.Target)

	return publishCmd
}

func runPublish(f *PublishFlags) error {
	target, err := filepath.Abs(f.Target)
	if err != nil {
		return fmt.Errorf("resolve target path: %w", err)
	}

	metadata, err := validatePublishTarget(target)
	if err != nil {
		return err
	}

	version := image.ResolvePublishVersion(metadata.Version)

	ref, err := image.ParsePublishReference(f.Registry, f.Namespace, metadata.ID, version)
	if err != nil {
		return err
	}

	log.Infof("publishing template %q to %s", metadata.ID, ref.String())

	img, err := image.BuildPublishImage("template", target)
	if err != nil {
		return err
	}

	if err := image.PushPublishImage("template", ref, img); err != nil {
		return err
	}

	return image.WritePublishedMetadata(os.Stdout, metadata)
}

func validatePublishTarget(target string) (*TemplateMetadata, error) {
	if err := image.ValidatePublishDir(target); err != nil {
		return nil, err
	}

	metadata, err := readTemplateMetadata(target)
	if err != nil {
		return nil, err
	}

	if metadata.ID == "" {
		return nil, fmt.Errorf("template metadata missing required 'id' field")
	}

	return metadata, nil
}
