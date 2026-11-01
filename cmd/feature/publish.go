package feature

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
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
		Short: "Package and push features to an OCI registry",
		Long: `Publish packaged dev container features to an OCI registry.

Takes the path to a packaged feature directory (output of 'features package'
or a directory containing devcontainer-feature.json) and pushes it as an
OCI artifact.`,
		SilenceUsage:  true,
		SilenceErrors: true,
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
			"Path to packaged feature directory",
		),
		cliflags.String(&publishFlags.Registry, names.Registry, "ghcr.io", "Target OCI registry"),
		cliflags.String(
			&publishFlags.Namespace,
			names.Namespace,
			"",
			"Registry namespace (e.g., devcontainers/features)",
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

	featureCfg, err := validatePublishTarget(target)
	if err != nil {
		return err
	}

	version := image.ResolvePublishVersion(featureCfg.Version)

	ref, err := image.ParsePublishReference(f.Registry, f.Namespace, featureCfg.ID, version)
	if err != nil {
		return err
	}

	log.Infof("publishing feature %q to %s", featureCfg.ID, ref.String())

	img, err := image.BuildPublishImage("feature", target)
	if err != nil {
		return err
	}

	if err := image.PushPublishImage("feature", ref, img); err != nil {
		return err
	}

	return image.WritePublishedMetadata(os.Stdout, publishedFeatureMetadata{
		ID:      featureCfg.ID,
		Version: version,
		Ref:     ref.String(),
	})
}

type publishedFeatureMetadata struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Ref     string `json:"ref"`
}

func validatePublishTarget(target string) (*config.FeatureConfig, error) {
	if err := image.ValidatePublishDir(target); err != nil {
		return nil, err
	}

	featureCfg, err := config.ParseDevContainerFeature(target)
	if err != nil {
		return nil, fmt.Errorf("parse feature metadata: %w", err)
	}

	return featureCfg, nil
}
