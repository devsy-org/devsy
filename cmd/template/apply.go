package template

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
	"github.com/tailscale/hujson"
)

const templateMetadataFile = "devcontainer-template.json"

type ApplyFlags struct {
	WorkspaceFolder string
	TemplateID      string
	TemplateArgs    []string
	Features        []string
	OmitPaths       []string
}

func NewApplyCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	applyFlags := &ApplyFlags{}
	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Fetch template from OCI registry and scaffold devcontainer config",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApply(applyFlags)
		},
	}

	applyCmd.Flags().StringVar(
		&applyFlags.WorkspaceFolder, "workspace-folder", ".",
		"Target workspace folder to scaffold into",
	)
	applyCmd.Flags().StringVar(
		&applyFlags.TemplateID, "template-id", "",
		"OCI reference of the template",
	)
	applyCmd.Flags().StringSliceVar(
		&applyFlags.TemplateArgs, "template-args", nil,
		"Template variable arguments as key=value pairs",
	)
	applyCmd.Flags().StringSliceVar(
		&applyFlags.Features, "features", nil,
		"Feature IDs to add to the generated devcontainer.json",
	)
	applyCmd.Flags().StringSliceVar(
		&applyFlags.OmitPaths, "omit-paths", nil,
		"Glob patterns for paths to skip during scaffolding",
	)
	_ = applyCmd.MarkFlagRequired("template-id")

	return applyCmd
}

func runApply(f *ApplyFlags) error {
	ref, err := name.ParseReference(f.TemplateID)
	if err != nil {
		return fmt.Errorf("parse template reference: %w", err)
	}

	log.Infof("Pulling template: %s", ref.String())

	img, err := pullTemplateImage(ref)
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp("", "devsy-template-*")
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

	templateVars := parseTemplateArgs(f.TemplateArgs, metadata)

	if err := applyTemplateSubstitution(tempDir, templateVars); err != nil {
		return err
	}

	return applyToWorkspace(f, tempDir)
}

func applyToWorkspace(f *ApplyFlags, tempDir string) error {
	workspaceFolder, err := filepath.Abs(f.WorkspaceFolder)
	if err != nil {
		return fmt.Errorf("resolve workspace folder: %w", err)
	}

	if err := copyTemplateFiles(tempDir, workspaceFolder, f.OmitPaths); err != nil {
		return err
	}

	if len(f.Features) > 0 {
		if err := addFeaturesToDevcontainer(workspaceFolder, f.Features); err != nil {
			return err
		}
	}

	log.Infof("Template applied successfully to %s", workspaceFolder)

	return nil
}

func pullTemplateImage(ref name.Reference) (v1.Image, error) {
	log.Debugf("fetching template OCI image: reference=%s", ref.String())

	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		err = image.SanitizeRegistryError(err)
		registry := ref.Context().RegistryStr()

		return nil, fmt.Errorf("pull template from %s: %w", registry, err)
	}

	return img, nil
}

func extractTemplateToDir(img v1.Image, destDir string) error {
	manifest, err := img.Manifest()
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	if len(manifest.Layers) == 0 {
		return fmt.Errorf("template image has no layers")
	}

	layer, err := img.LayerByDigest(manifest.Layers[0].Digest)
	if err != nil {
		return fmt.Errorf("retrieve template layer: %w", err)
	}

	data, err := layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("read template layer: %w", err)
	}
	defer func() { _ = data.Close() }()

	if err := extract.Extract(data, destDir); err != nil {
		return fmt.Errorf("extract template: %w", err)
	}

	return nil
}

type TemplateMetadata struct {
	ID          string                    `json:"id"`
	Version     string                    `json:"version"`
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Options     map[string]TemplateOption `json:"options,omitempty"`
}

type TemplateOption struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Default     any    `json:"default,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
}

func readTemplateMetadata(templateDir string) (*TemplateMetadata, error) {
	metadataPath := filepath.Join(templateDir, templateMetadataFile)

	data, err := os.ReadFile(filepath.Clean(metadataPath))
	if err != nil {
		if os.IsNotExist(err) {
			return &TemplateMetadata{}, nil
		}

		return nil, fmt.Errorf("read template metadata: %w", err)
	}

	var metadata TemplateMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("parse template metadata: %w", err)
	}

	return &metadata, nil
}

func parseTemplateArgs(args []string, metadata *TemplateMetadata) map[string]string {
	vars := make(map[string]string)

	if metadata != nil && metadata.Options != nil {
		for key, opt := range metadata.Options {
			if opt.Default != nil {
				vars[key] = fmt.Sprintf("%v", opt.Default)
			}
		}
	}

	for _, arg := range args {
		key, value, found := strings.Cut(arg, "=")
		if found {
			vars[key] = value
		}
	}

	return vars
}

func applyTemplateSubstitution(templateDir string, vars map[string]string) error {
	root, err := os.OpenRoot(templateDir)
	if err != nil {
		return fmt.Errorf("open template root: %w", err)
	}
	defer func() { _ = root.Close() }()

	return filepath.Walk(templateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || info.Name() == templateMetadataFile {
			return nil
		}

		relPath, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}

		return substituteFileVars(root, relPath, info, vars)
	})
}

func substituteFileVars(
	root *os.Root, relPath string, info os.FileInfo, vars map[string]string,
) error {
	data, err := root.ReadFile(relPath)
	if err != nil {
		return err
	}

	content := string(data)
	modified := false

	for key, value := range vars {
		placeholder := "${templateOption:" + key + "}"
		if strings.Contains(content, placeholder) {
			content = strings.ReplaceAll(content, placeholder, value)
			modified = true
		}
	}

	if modified {
		return root.WriteFile(relPath, []byte(content), info.Mode())
	}

	return nil
}

func copyTemplateFiles(srcDir, destDir string, omitPaths []string) error {
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return fmt.Errorf("create workspace folder: %w", err)
	}

	destRoot, err := os.OpenRoot(destDir)
	if err != nil {
		return fmt.Errorf("open dest root: %w", err)
	}
	defer func() { _ = destRoot.Close() }()

	copier := &templateCopier{destDir: destDir, destRoot: destRoot, omitPaths: omitPaths}

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		if relPath == "." || info.Name() == templateMetadataFile {
			return nil
		}

		return copier.copyEntry(path, relPath, info)
	})
}

type templateCopier struct {
	destDir   string
	destRoot  *os.Root
	omitPaths []string
}

func (c *templateCopier) copyEntry(path, relPath string, info os.FileInfo) error {
	if shouldOmit(relPath, c.omitPaths) {
		if info.IsDir() {
			return filepath.SkipDir
		}

		return nil
	}

	destPath := filepath.Join(c.destDir, relPath)

	if info.IsDir() {
		return os.MkdirAll(destPath, 0o750)
	}

	return copyFile(path, c.destRoot, relPath, info.Mode())
}

func copyFile(srcPath string, destRoot *os.Root, destRelPath string, mode os.FileMode) error {
	data, err := os.ReadFile(filepath.Clean(srcPath))
	if err != nil {
		return err
	}

	destDir := filepath.Dir(destRelPath)
	if destDir != "." {
		if err := destRoot.Mkdir(destDir, 0o750); err != nil && !os.IsExist(err) {
			return err
		}
	}

	return destRoot.WriteFile(destRelPath, data, mode&0o666)
}

func shouldOmit(relPath string, omitPaths []string) bool {
	for _, pattern := range omitPaths {
		matched, err := doublestar.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}
	}

	return false
}

func addFeaturesToDevcontainer(workspaceFolder string, features []string) error {
	devcontainerPath := findDevcontainerJSON(workspaceFolder)
	if devcontainerPath == "" {
		log.Warnf("no devcontainer.json found in workspace, --features not applied")
		return nil
	}

	data, err := os.ReadFile(filepath.Clean(devcontainerPath))
	if err != nil {
		return fmt.Errorf("read devcontainer.json: %w", err)
	}

	normalized, err := hujson.Standardize(data)
	if err != nil {
		return fmt.Errorf("parse devcontainer.json: %w", err)
	}

	var config map[string]any
	if err := json.Unmarshal(normalized, &config); err != nil {
		return fmt.Errorf("parse devcontainer.json: %w", err)
	}

	featureMap, ok := config["features"].(map[string]any)
	if !ok {
		featureMap = make(map[string]any)
	}

	for _, featureID := range features {
		featureMap[featureID] = map[string]any{}
	}

	config["features"] = featureMap

	output, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		return fmt.Errorf("marshal devcontainer.json: %w", err)
	}

	// #nosec G306 -- devcontainer.json needs to be readable
	return os.WriteFile(devcontainerPath, output, 0o644)
}

func findDevcontainerJSON(workspaceFolder string) string {
	path := filepath.Join(workspaceFolder, ".devcontainer", "devcontainer.json")
	if _, err := os.Stat(path); err == nil {
		return path
	}

	path = filepath.Join(workspaceFolder, ".devcontainer.json")
	if _, err := os.Stat(path); err == nil {
		return path
	}

	return ""
}
