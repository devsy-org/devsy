package feature

import (
	"fmt"
	"os"
	"strconv"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/spf13/cobra"
)

type InfoManifestCmd struct {
	*flags.GlobalFlags
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

	return manifestCmd
}

func (cmd *InfoManifestCmd) Run(featureID string) error {
	ref, err := name.ParseReference(featureID)
	if err != nil {
		return fmt.Errorf("invalid feature reference %q: %w", featureID, err)
	}

	manifest, err := feature.FetchOCIManifest(ref)
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModeJSON:
		return writeJSON(os.Stdout, manifest)
	case output.ModePlain:
		return cmd.printText(manifest)
	}
	return nil
}

func (cmd *InfoManifestCmd) printText(manifest *v1.Manifest) error {
	w := os.Stdout

	table.Print([]string{"Property", headerValue}, [][]string{
		{"Schema Version", strconv.FormatInt(manifest.SchemaVersion, 10)},
		{"Media Type", string(manifest.MediaType)},
		{"Config Media Type", string(manifest.Config.MediaType)},
		{"Config Digest", manifest.Config.Digest.String()},
		{"Config Size", strconv.FormatInt(manifest.Config.Size, 10)},
	})

	if len(manifest.Layers) > 0 {
		_, _ = fmt.Fprintln(w, "\nLayers:")
		layerRows := make([][]string, 0, len(manifest.Layers))
		for i, layer := range manifest.Layers {
			layerRows = append(layerRows, []string{
				strconv.Itoa(i),
				string(layer.MediaType),
				layer.Digest.String(),
				strconv.FormatInt(layer.Size, 10),
			})
		}
		table.Print([]string{"#", "Media Type", "Digest", "Size"}, layerRows)
	}

	if len(manifest.Annotations) > 0 {
		_, _ = fmt.Fprintln(w, "\nAnnotations:")
		annoRows := make([][]string, 0, len(manifest.Annotations))
		for k, v := range manifest.Annotations {
			annoRows = append(annoRows, []string{k, v})
		}
		table.Print([]string{"Key", headerValue}, annoRows)
	}

	return nil
}
