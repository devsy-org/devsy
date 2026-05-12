package features

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"
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
		Short: "Package and push features to OCI registry",
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

	publishCmd.Flags().StringVar(
		&publishFlags.Target, "target", "",
		"Path to packaged feature directory",
	)
	publishCmd.Flags().StringVar(
		&publishFlags.Registry, "registry", "ghcr.io",
		"Target OCI registry",
	)
	publishCmd.Flags().StringVar(
		&publishFlags.Namespace, "namespace", "",
		"Registry namespace (e.g., devcontainers/features)",
	)
	_ = publishCmd.MarkFlagRequired("target")

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

	version := featureCfg.Version
	if version == "" {
		version = "latest"
	}

	ref, err := parsePublishRef(f.Registry, f.Namespace, featureCfg.ID, version)
	if err != nil {
		return err
	}

	log.Infof("Publishing feature %q to %s", featureCfg.ID, ref.String())

	img, err := buildFeatureImage(target)
	if err != nil {
		return err
	}

	if err := remote.Write(
		ref, img, remote.WithAuthFromKeychain(authn.DefaultKeychain),
	); err != nil {
		return fmt.Errorf("push feature to registry: %w", err)
	}

	log.Infof("Feature published successfully: %s", ref.String())

	metadata := publishedFeatureMetadata{
		ID:      featureCfg.ID,
		Version: version,
		Ref:     ref.String(),
	}

	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal published metadata: %w", err)
	}

	_, _ = os.Stdout.Write(metadataJSON)
	_, _ = os.Stdout.WriteString("\n")

	return nil
}

type publishedFeatureMetadata struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Ref     string `json:"ref"`
}

func validatePublishTarget(target string) (*config.FeatureConfig, error) {
	stat, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("stat target: %w", err)
	}

	if !stat.IsDir() {
		return nil, fmt.Errorf("target must be a directory: %s", target)
	}

	featureCfg, err := config.ParseDevContainerFeature(target)
	if err != nil {
		return nil, fmt.Errorf("parse feature metadata: %w", err)
	}

	if featureCfg.ID == "" {
		return nil, fmt.Errorf("feature metadata missing required 'id' field")
	}

	return featureCfg, nil
}

func parsePublishRef(
	registry, namespace, id, version string,
) (name.Reference, error) {
	refStr := buildPublishReference(registry, namespace, id, version)

	ref, err := name.ParseReference(refStr)
	if err != nil {
		return nil, fmt.Errorf("parse publish reference %q: %w", refStr, err)
	}

	return ref, nil
}

func buildPublishReference(registry, namespace, id, version string) string {
	if namespace != "" {
		return fmt.Sprintf("%s/%s/%s:%s", registry, namespace, id, version)
	}

	return fmt.Sprintf("%s/%s:%s", registry, id, version)
}

func buildFeatureImage(sourceDir string) (v1.Image, error) {
	var buf bytes.Buffer
	if err := extract.WriteTar(&buf, sourceDir, true); err != nil {
		return nil, fmt.Errorf("create feature archive: %w", err)
	}

	layer := stream.NewLayer(
		io.NopCloser(bytes.NewReader(buf.Bytes())),
		stream.WithMediaType(types.OCILayer),
	)

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return nil, fmt.Errorf("build feature image: %w", err)
	}

	return img, nil
}
