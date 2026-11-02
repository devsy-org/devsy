package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const (
	// ProviderAssetsDirName is the installed provider's icon cache directory.
	ProviderAssetsDirName = "provider-assets"

	iconThemeLight = "light"
	iconThemeDark  = "dark"

	iconCacheSchemaVersion = 1
	iconBlobsDir           = "blobs"
	iconRevisionsDir       = "revisions"
	maxRevisionMetadata    = 64 << 10
)

var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ProviderIconResult is the disk-only answer to a provider icon read.
type ProviderIconResult struct {
	Revision   string `json:"revision"`
	MimeType   string `json:"mimeType,omitempty"`
	DataBase64 string `json:"dataBase64,omitempty"`
	Missing    bool   `json:"missing,omitempty"`
}

// iconInputs are everything that identifies where a revision came from.
type iconInputs struct {
	manifest []byte
	source   ProviderSource
	icon     string
	iconDark string
}

// iconVariantMetadata describes one theme variant of a revision. File is the
// cache-relative slash-separated blob name.
type iconVariantMetadata struct {
	Missing  bool   `json:"missing,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	File     string `json:"file,omitempty"`
}

// iconRevisionMetadata is the immutable record an installed manifest selects by
// its hash. It deliberately carries no revision field of its own.
type iconRevisionMetadata struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Fingerprint   string              `json:"fingerprint"`
	Light         iconVariantMetadata `json:"light"`
	Dark          iconVariantMetadata `json:"dark"`
}

func iconFingerprint(inputs iconInputs) (string, error) {
	manifestHash := sha256.Sum256(inputs.manifest)
	payload, err := json.Marshal(struct {
		Manifest string         `json:"manifest"`
		Source   ProviderSource `json:"source"`
		Icon     string         `json:"icon"`
		IconDark string         `json:"iconDark"`
	}{
		Manifest: hex.EncodeToString(manifestHash[:]),
		Source:   inputs.source,
		Icon:     inputs.icon,
		IconDark: inputs.iconDark,
	})
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func hashIconRevision(metadata iconRevisionMetadata) (string, error) {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func variantMetadata(variant iconVariant) iconVariantMetadata {
	if variant.data == nil {
		return iconVariantMetadata{Missing: true}
	}

	sum := sha256.Sum256(variant.data)
	digest := hex.EncodeToString(sum[:])
	return iconVariantMetadata{
		MimeType: variant.mime,
		SHA256:   digest,
		File:     iconBlobsDir + "/" + digest,
	}
}

// storeIconRevision writes the content-addressed blobs first and the revision
// metadata last, so a revision visible on disk is always complete. created is
// false when the identical revision was already recorded.
func storeIconRevision(
	cacheDir string,
	inputs iconInputs,
	light, dark iconVariant,
) (revision string, created bool, err error) {
	fingerprint, err := iconFingerprint(inputs)
	if err != nil {
		return "", false, fmt.Errorf("fingerprint provider icons: %w", err)
	}

	metadata := iconRevisionMetadata{
		SchemaVersion: iconCacheSchemaVersion,
		Fingerprint:   fingerprint,
		Light:         variantMetadata(light),
		Dark:          variantMetadata(dark),
	}
	revision, err = hashIconRevision(metadata)
	if err != nil {
		return "", false, fmt.Errorf("hash provider icon revision: %w", err)
	}

	if err := storeIconBlobs(cacheDir, metadata, light, dark); err != nil {
		return "", false, err
	}

	revisionFile := filepath.Join(cacheDir, iconRevisionsDir, revision+".json")
	if existingRevisionVerifies(cacheDir, revision) {
		return revision, false, nil
	}

	payload, err := json.Marshal(metadata)
	if err != nil {
		return "", false, fmt.Errorf("encode provider icon revision: %w", err)
	}
	if err := WriteFileAtomic(revisionFile, payload, 0o600); err != nil {
		return "", false, fmt.Errorf("write provider icon revision: %w", err)
	}

	return revision, true, nil
}

func existingRevisionVerifies(cacheDir, revision string) bool {
	root, err := os.OpenRoot(cacheDir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	_, err = loadIconRevision(root, revision)
	return err == nil
}

func storeIconBlobs(
	cacheDir string,
	metadata iconRevisionMetadata,
	light, dark iconVariant,
) error {
	blobsDir := filepath.Join(cacheDir, iconBlobsDir)
	for _, dir := range []string{blobsDir, filepath.Join(cacheDir, iconRevisionsDir)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create provider icon cache: %w", err)
		}
	}
	for _, stored := range []struct {
		meta    iconVariantMetadata
		variant iconVariant
	}{{metadata.Light, light}, {metadata.Dark, dark}} {
		if stored.meta.Missing {
			continue
		}
		if err := WriteFileAtomic(
			filepath.Join(blobsDir, stored.meta.SHA256),
			stored.variant.data,
			0o600,
		); err != nil {
			return fmt.Errorf("write provider icon: %w", err)
		}
	}
	return nil
}

// ReadProviderIcon returns the cached icon variant selected by the installed
// manifest. It reads disk only, never the network. An installation without a
// revision or an explicitly missing variant returns Missing; absent selected
// cache files and other I/O or integrity failures are errors.
func ReadProviderIcon(
	devsyContext string,
	cfg *ProviderConfig,
	theme string,
) (*ProviderIconResult, error) {
	if err := validateIconRead(cfg, theme); err != nil {
		return nil, err
	}
	if cfg.IconRevision == "" {
		return &ProviderIconResult{Missing: true}, nil
	}

	cacheDir, err := GetProviderAssetsDir(devsyContext, cfg.Name)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("open provider icon cache: %w", err)
	}
	defer func() { _ = root.Close() }()
	return readCachedProviderIcon(root, cfg.IconRevision, theme)
}

func validateIconRead(cfg *ProviderConfig, theme string) error {
	if theme != iconThemeLight && theme != iconThemeDark {
		return fmt.Errorf("unsupported icon theme %q: use light or dark", theme)
	}
	if cfg == nil {
		return errors.New("provider config is nil")
	}
	if cfg.IconRevision != "" && !sha256HexPattern.MatchString(cfg.IconRevision) {
		return fmt.Errorf("invalid icon revision %q", cfg.IconRevision)
	}
	return nil
}

func readCachedProviderIcon(root *os.Root, revision, theme string) (*ProviderIconResult, error) {
	metadata, err := loadIconRevision(root, revision)
	if err != nil {
		return nil, err
	}

	variant := metadata.Light
	if theme == iconThemeDark {
		variant = metadata.Dark
	}
	if variant.Missing {
		return &ProviderIconResult{Revision: revision, Missing: true}, nil
	}

	data, err := loadIconBlob(root, variant)
	if err != nil {
		return nil, err
	}

	return &ProviderIconResult{
		Revision:   revision,
		MimeType:   variant.MimeType,
		DataBase64: base64.StdEncoding.EncodeToString(data),
	}, nil
}

func loadIconRevision(root *os.Root, revision string) (*iconRevisionMetadata, error) {
	file, err := root.Open(iconRevisionsDir + "/" + revision + ".json")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", "open provider icon revision", err)
	}
	defer func() { _ = file.Close() }()

	payload, err := io.ReadAll(io.LimitReader(file, maxRevisionMetadata+1))
	if err != nil {
		return nil, fmt.Errorf("read provider icon revision: %w", err)
	}
	if len(payload) > maxRevisionMetadata {
		return nil, errors.New("provider icon revision metadata is too large")
	}

	metadata := &iconRevisionMetadata{}
	if err := json.Unmarshal(payload, metadata); err != nil {
		return nil, fmt.Errorf("decode provider icon revision: %w", err)
	}
	if metadata.SchemaVersion != iconCacheSchemaVersion {
		return nil, fmt.Errorf("unsupported provider icon schema %d", metadata.SchemaVersion)
	}

	recomputed, err := hashIconRevision(*metadata)
	if err != nil {
		return nil, err
	}
	if recomputed != revision {
		return nil, errors.New("provider icon revision metadata does not match its revision")
	}

	return metadata, nil
}

func loadIconBlob(root *os.Root, variant iconVariantMetadata) ([]byte, error) {
	if !validIconBlobMetadata(variant) {
		return nil, errors.New("provider icon metadata names an invalid cache entry")
	}

	file, err := root.Open(variant.File)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", "open provider icon", err)
	}
	defer func() { _ = file.Close() }()

	data, err := readIconBlob(file)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != variant.SHA256 {
		return nil, errors.New("cached provider icon does not match its recorded hash")
	}
	return data, nil
}

func validIconBlobMetadata(variant iconVariantMetadata) bool {
	return sha256HexPattern.MatchString(variant.SHA256) &&
		variant.File == iconBlobsDir+"/"+variant.SHA256 &&
		isAllowedIconMime(variant.MimeType)
}

func readIconBlob(file *os.File) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat provider icon: %w", err)
	}
	if info.Size() > maxIconBytes {
		return nil, fmt.Errorf("cached provider icon exceeds %d bytes", maxIconBytes)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxIconBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read provider icon: %w", err)
	}
	if len(data) > maxIconBytes {
		return nil, fmt.Errorf("cached provider icon exceeds %d bytes", maxIconBytes)
	}

	return data, nil
}

// CopyProviderIcons copies the selected revision into the clone's own cache.
// Blobs precede metadata, and the caller publishes the destination manifest last.
// Missing source files, I/O failures and integrity errors are returned.
func CopyProviderIcons(
	ctx context.Context,
	devsyContext, sourceName string,
	destination *ProviderConfig,
) error {
	if destination.IconRevision == "" {
		return nil
	}
	if !sha256HexPattern.MatchString(destination.IconRevision) {
		return errors.New("invalid provider icon revision")
	}
	sourceDir, err := GetProviderAssetsDir(devsyContext, sourceName)
	if err != nil {
		return err
	}
	source, err := os.OpenRoot(sourceDir)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	destinationDir, err := GetProviderAssetsDir(devsyContext, destination.Name)
	if err != nil {
		return err
	}
	return copyCachedIconRevision(ctx, source, destinationDir, destination.IconRevision)
}

func copyCachedIconRevision(
	ctx context.Context,
	source *os.Root,
	destinationDir, revision string,
) error {
	metadata, err := loadIconRevision(source, revision)
	if err != nil {
		return err
	}
	for _, variant := range []iconVariantMetadata{metadata.Light, metadata.Dark} {
		if err := copyIconVariant(ctx, source, destinationDir, variant); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(destinationDir, iconRevisionsDir), 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return WriteFileAtomic(
		filepath.Join(destinationDir, iconRevisionsDir, revision+".json"),
		payload,
		0o600,
	)
}

func copyIconVariant(
	ctx context.Context,
	source *os.Root,
	destinationDir string,
	variant iconVariantMetadata,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if variant.Missing {
		return nil
	}
	data, err := loadIconBlob(source, variant)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(destinationDir, iconBlobsDir), 0o700); err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(destinationDir, variant.File), data, 0o600)
}
