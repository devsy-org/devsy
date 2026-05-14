package features

import (
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/spf13/cobra"
)

type InfoManifestCmd struct {
	*flags.GlobalFlags

	Output string
}

func NewInfoManifestCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &InfoManifestCmd{GlobalFlags: globalFlags}
	manifestCmd := &cobra.Command{
		Use:   "manifest <feature-id>",
		Short: "Display the OCI manifest for a published feature",
		Long: `Fetch and display the raw OCI manifest for a published dev container feature.

Accepts a feature ID like ghcr.io/devcontainers/features/go or
ghcr.io/devcontainers/features/go:1 and outputs the OCI image manifest.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.Run(args[0])
		},
	}

	manifestCmd.Flags().StringVar(&cmd.Output, "output", "json", "Output format (text or json)")

	return manifestCmd
}

func (cmd *InfoManifestCmd) Run(featureID string) error {
	if err := validateOutputFormat(cmd.Output); err != nil {
		return err
	}

	ref, err := name.ParseReference(featureID)
	if err != nil {
		return fmt.Errorf("invalid feature reference %q: %w", featureID, err)
	}

	manifest, err := feature.FetchOCIManifest(ref)
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}

	if cmd.Output == outputJSON {
		return writeJSON(os.Stdout, manifest)
	}
	return cmd.printText(manifest)
}

func (cmd *InfoManifestCmd) printText(manifest *v1.Manifest) error {
	w := os.Stdout
	_, _ = fmt.Fprintf(w, "Schema Version: %d\n", manifest.SchemaVersion)
	_, _ = fmt.Fprintf(w, "Media Type: %s\n", manifest.MediaType)

	_, _ = fmt.Fprintf(w, "\nConfig:\n")
	_, _ = fmt.Fprintf(w, "  Media Type: %s\n", manifest.Config.MediaType)
	_, _ = fmt.Fprintf(w, "  Digest: %s\n", manifest.Config.Digest)
	_, _ = fmt.Fprintf(w, "  Size: %d\n", manifest.Config.Size)

	if len(manifest.Layers) > 0 {
		_, _ = fmt.Fprintf(w, "\nLayers:\n")
		for i, layer := range manifest.Layers {
			_, _ = fmt.Fprintf(w, "  [%d] Media Type: %s\n", i, layer.MediaType)
			_, _ = fmt.Fprintf(w, "      Digest: %s\n", layer.Digest)
			_, _ = fmt.Fprintf(w, "      Size: %d\n", layer.Size)
		}
	}

	if len(manifest.Annotations) > 0 {
		_, _ = fmt.Fprintf(w, "\nAnnotations:\n")
		for k, v := range manifest.Annotations {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", k, v)
		}
	}

	return nil
}
