package features

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/spf13/cobra"
)

type PackageCmd struct {
	*flags.GlobalFlags

	Target                 string
	OutputFolder           string
	ForceCleanOutputFolder bool
	Output                 string
}

type packageResult struct {
	FeatureID  string `json:"featureId"`
	Version    string `json:"version"`
	Filename   string `json:"filename"`
	OutputPath string `json:"outputPath"`
}

func NewPackageCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &PackageCmd{GlobalFlags: globalFlags}
	packageCmd := &cobra.Command{
		Use:   "package",
		Short: "Package feature source directories into OCI-compliant tarballs",
		Long: `Bundle feature source directories into devcontainer-feature-<id>.tgz archives.

Scans the target directory for subdirectories containing devcontainer-feature.json
and creates gzipped tar archives suitable for OCI distribution.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return cmd.Run()
		},
	}

	packageCmd.Flags().StringVar(
		&cmd.Target, "target", "",
		"Path to directory containing feature source subdirectories",
	)
	packageCmd.Flags().StringVar(
		&cmd.OutputFolder, "output-folder", ".",
		"Where to write the .tgz files",
	)
	packageCmd.Flags().BoolVar(
		&cmd.ForceCleanOutputFolder, "force-clean-output-folder", false,
		"Clean output folder before writing",
	)
	packageCmd.Flags().StringVar(
		&cmd.Output, "output", "text",
		"Output format (text or json)",
	)
	_ = packageCmd.MarkFlagRequired("target")

	return packageCmd
}

func (cmd *PackageCmd) Run() error {
	if err := validateOutputFormat(cmd.Output); err != nil {
		return err
	}

	target, err := filepath.Abs(cmd.Target)
	if err != nil {
		return fmt.Errorf("resolve target path: %w", err)
	}

	outputFolder, err := cmd.prepareOutputFolder()
	if err != nil {
		return err
	}

	features, err := cmd.discoverFeatures(target)
	if err != nil {
		return err
	}

	var results []packageResult
	for _, feat := range features {
		result, pkgErr := cmd.packageFeature(feat, target, outputFolder)
		if pkgErr != nil {
			return pkgErr
		}
		results = append(results, result)
	}

	if cmd.Output == outputJSON {
		return writeJSON(os.Stdout, results)
	}

	return cmd.printResults(results)
}

func (cmd *PackageCmd) prepareOutputFolder() (string, error) {
	outputFolder, err := filepath.Abs(cmd.OutputFolder)
	if err != nil {
		return "", fmt.Errorf("resolve output folder: %w", err)
	}

	if cmd.ForceCleanOutputFolder {
		if err := os.RemoveAll(outputFolder); err != nil {
			return "", fmt.Errorf("clean output folder: %w", err)
		}
	}

	// #nosec G301 -- output directory needs to be accessible
	if err := os.MkdirAll(outputFolder, 0o755); err != nil {
		return "", fmt.Errorf("create output folder: %w", err)
	}

	return outputFolder, nil
}

type featureSource struct {
	dir    string
	config *config.FeatureConfig
}

func (cmd *PackageCmd) discoverFeatures(targetDir string) ([]featureSource, error) {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil, fmt.Errorf("read target directory: %w", err)
	}

	var features []featureSource
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		featureDir := filepath.Join(targetDir, entry.Name())
		featureCfg, parseErr := config.ParseDevContainerFeature(featureDir)
		if parseErr != nil {
			continue
		}

		features = append(features, featureSource{
			dir:    entry.Name(),
			config: featureCfg,
		})
	}

	if len(features) == 0 {
		return nil, fmt.Errorf("no features found in %s", targetDir)
	}
	return features, nil
}

func (cmd *PackageCmd) packageFeature(
	feat featureSource, targetDir, outputFolder string,
) (packageResult, error) {
	featureDir := filepath.Join(targetDir, feat.dir)
	filename := fmt.Sprintf("devcontainer-feature-%s.tgz", feat.config.ID)
	outputPath := filepath.Join(outputFolder, filename)

	f, err := os.Create(outputPath) // #nosec G304 -- path constructed from validated inputs
	if err != nil {
		return packageResult{}, fmt.Errorf("create archive %s: %w", filename, err)
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	if err := extract.WriteTar(&buf, featureDir, true); err != nil {
		return packageResult{}, fmt.Errorf("create tar for %s: %w", feat.config.ID, err)
	}

	if _, err := f.Write(buf.Bytes()); err != nil {
		return packageResult{}, fmt.Errorf("write archive %s: %w", filename, err)
	}

	return packageResult{
		FeatureID:  feat.config.ID,
		Version:    feat.config.Version,
		Filename:   filename,
		OutputPath: outputPath,
	}, nil
}

func (cmd *PackageCmd) printResults(results []packageResult) error {
	w := os.Stdout
	_, _ = fmt.Fprintln(w, "Packaged dev container features:")
	for _, r := range results {
		version := r.Version
		if version == "" {
			version = "(no version)"
		}
		_, _ = fmt.Fprintf(w, "  %s (%s) -> %s\n", r.FeatureID, version, r.OutputPath)
	}
	_, _ = fmt.Fprintf(w, "\nTotal: %d feature(s) packaged\n", len(results))
	return nil
}
