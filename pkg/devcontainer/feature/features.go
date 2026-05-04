package feature

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/hash"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const DEVCONTAINER_MANIFEST_MEDIATYPE = "application/vnd.devcontainers"

var directTarballRegEx = regexp.MustCompile("devcontainer-feature-([a-zA-Z0-9_-]+).tgz")

func getFeatureInstallWrapperScript(
	idWithoutVersion string,
	feature *config.FeatureConfig,
	options []string,
) string {
	id := escapeQuotesForShell(idWithoutVersion)
	name := escapeQuotesForShell(feature.Name)
	description := escapeQuotesForShell(feature.Description)
	version := escapeQuotesForShell(feature.Version)
	documentation := escapeQuotesForShell(feature.DocumentationURL)
	maskedOptions := maskSecretOptions(feature, options)
	optionsIndented := escapeQuotesForShell("    " + strings.Join(maskedOptions, "\n    "))

	warningHeader := ""
	if feature.Deprecated {
		warningHeader += `(!) WARNING: Using the deprecated Feature ` +
			`"${escapeQuotesForShell(feature.id)}". This Feature will no longer receive any further updates/support.\n`
	}

	echoWarning := ""
	if warningHeader != "" {
		echoWarning = `echo '` + warningHeader + `'`
	}

	errorMessage := `ERROR: Feature "` + name + `" (` + id + `) failed to install!`
	troubleshootingMessage := ""
	if documentation != "" {
		troubleshootingMessage = ` Look at the documentation at ${documentation} for help troubleshooting this error.`
	}

	return `#!/bin/sh
set -e

on_exit () {
	[ $? -eq 0 ] && exit
	echo '` + errorMessage + troubleshootingMessage + `'
}

trap on_exit EXIT

set -a
. ../devcontainer-features.builtin.env
. ./devcontainer-features.env
set +a

echo ===========================================================================
` + echoWarning + `
echo 'Feature       : ` + name + `'
echo 'Description   : ` + description + `'
echo 'Id            : ` + id + `'
echo 'Version       : ` + version + `'
echo 'Documentation : ` + documentation + `'
echo 'Options       :'
echo '` + optionsIndented + `'
echo ===========================================================================

chmod +x ./install.sh
./install.sh
`
}

func escapeQuotesForShell(str string) string {
	// The `input` is expected to be a string which will be printed inside single quotes
	// by the caller. This means we need to escape any nested single quotes within the string.
	// We can do this by ending the first string with a single quote ('), printing an escaped
	// single quote (\'), and then opening a new string (').
	return strings.ReplaceAll(str, "'", `'\''`)
}

func maskSecretOptions(feature *config.FeatureConfig, options []string) []string {
	if feature.Options == nil {
		return options
	}

	secretKeys := make(map[string]bool)
	for name, opt := range feature.Options {
		if opt.Type == optionTypeSecret {
			secretKeys[getFeatureSafeID(name)] = true
		}
	}

	if len(secretKeys) == 0 {
		return options
	}

	masked := make([]string, len(options))
	for i, opt := range options {
		key, _, found := strings.Cut(opt, "=")
		if found && secretKeys[key] {
			masked[i] = key + `="****"`
		} else {
			masked[i] = opt
		}
	}
	return masked
}

func ProcessFeatureID(
	id string,
	devContainerConfig *config.DevContainerConfig,
	forceBuild bool,
) (string, error) {
	if strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "http://") {
		log.Debugf("process feature: type=%s, id=%s", "url", id)
		return processDirectTarFeature(
			id,
			config.GetDevsyCustomizations(devContainerConfig).FeatureDownloadHTTPHeaders,
			forceBuild,
		)
	} else if strings.HasPrefix(id, "./") || strings.HasPrefix(id, "../") {
		log.Debugf("process feature: type=%s, id=%s", "local", id)
		return filepath.Abs(
			path.Join(filepath.ToSlash(filepath.Dir(devContainerConfig.Origin)), id),
		)
	}

	// get oci feature
	log.Debugf("process feature: type=%s, id=%s", "oci", id)
	return processOCIFeature(id)
}

func checkFeatureCache(id string) (string, bool) {
	featureFolder, err := getFeaturesTempFolder(id)
	if err != nil {
		log.Debugf("failed to resolve feature cache dir: %v", err)
		return "", false
	}

	featureExtractedFolder := filepath.Join(featureFolder, "extracted")
	_, err = os.Stat(featureExtractedFolder)
	if err == nil {
		// make sure feature.json is there as well
		_, err = os.Stat(
			filepath.Join(featureExtractedFolder, config.DEVCONTAINER_FEATURE_FILE_NAME),
		)
		if err == nil {
			log.Debugf("feature already cached: folder=%s", featureExtractedFolder)
			return featureExtractedFolder, true
		} else {
			log.Debugf("feature folder exists but seems empty: folder=%s", featureExtractedFolder)
			_ = os.RemoveAll(featureFolder)
		}
	}
	return "", false
}

func pullOCIImage(ref name.Reference) (v1.Image, error) {
	var img v1.Image
	err := retryOCIPull(func() error {
		log.Debugf("fetching OCI image: reference=%s", ref.String())
		var fetchErr error
		img, fetchErr = remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
		return fetchErr
	})
	if err != nil {
		err = image.SanitizeRegistryError(err)
		registry := sanitizeURL(ref.Context().RegistryStr())
		log.Debugf("failed to fetch OCI image: error=%v, registry=%s", err, registry)
		return nil, fmt.Errorf("pull from %s: %w", registry, err)
	}
	return img, nil
}

// PullFeatureToTemp pulls an OCI feature image and extracts it to a temporary folder.
// Returns the path to the extracted feature folder.
func PullFeatureToTemp(ref name.Reference, id string) (string, error) {
	featureFolder, err := getFeaturesTempFolder(id)
	if err != nil {
		return "", fmt.Errorf("resolve feature cache dir: %w", err)
	}

	featureExtractedFolder := filepath.Join(featureFolder, "extracted")

	annotations, err := pullAndExtractOCIFeature(ref, id, featureFolder, featureExtractedFolder)
	if err != nil {
		return "", err
	}

	if len(annotations) > 0 {
		logOCIAnnotations(id, annotations)
		saveAnnotations(featureFolder, annotations)
	}

	return featureExtractedFolder, nil
}

func processOCIFeature(id string) (string, error) {
	log.Debugf("processing OCI feature: featureId=%s", id)

	if cached, ok := checkFeatureCache(id); ok {
		return cached, nil
	}

	featureFolder, err := getFeaturesTempFolder(id)
	if err != nil {
		return "", fmt.Errorf("resolve feature cache dir: %w", err)
	}

	featureExtractedFolder := filepath.Join(featureFolder, "extracted")

	ref, err := name.ParseReference(id)
	if err != nil {
		log.Debugf("failed to parse OCI reference: error=%v, featureId=%s", err, id)
		return "", err
	}

	annotations, err := pullAndExtractOCIFeature(ref, id, featureFolder, featureExtractedFolder)
	if err != nil {
		return "", err
	}

	if len(annotations) > 0 {
		logOCIAnnotations(id, annotations)
		saveAnnotations(featureFolder, annotations)
	}

	log.Infof(
		"OCI feature processed successfully: featureId=%s, path=%s",
		id,
		featureExtractedFolder,
	)
	return featureExtractedFolder, nil
}

const annotationsFileName = "annotations.json"

func logOCIAnnotations(id string, annotations map[string]string) {
	title := annotations["org.opencontainers.image.title"]
	description := annotations["org.opencontainers.image.description"]
	version := annotations["org.opencontainers.image.version"]

	if title != "" || description != "" {
		log.Infof(
			"Feature %q: title=%q, description=%q, version=%q",
			id, title, description, version,
		)
	}

	for key, value := range annotations {
		log.Debugf("OCI annotation: featureId=%s, %s=%s", id, key, value)
	}
}

func saveAnnotations(featureFolder string, annotations map[string]string) {
	data, err := json.Marshal(annotations)
	if err != nil {
		log.Debugf("failed to marshal annotations: %v", err)
		return
	}
	filePath := filepath.Join(featureFolder, annotationsFileName)
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		log.Debugf("failed to write annotations sidecar: %v", err)
	}
}

func LoadOCIAnnotations(featureFolder string) map[string]string {
	parentDir := filepath.Dir(featureFolder)
	filePath := filepath.Join(parentDir, annotationsFileName)
	data, err := os.ReadFile(filepath.Clean(filePath))
	if err != nil {
		return nil
	}
	var annotations map[string]string
	if err := json.Unmarshal(data, &annotations); err != nil {
		log.Debugf("failed to parse annotations sidecar: %v", err)
		return nil
	}
	return annotations
}

func pullAndExtractOCIFeature(
	ref name.Reference,
	id, featureFolder, destDir string,
) (map[string]string, error) {
	img, err := pullOCIImage(ref)
	if err != nil {
		return nil, err
	}

	manifest, err := img.Manifest()
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	destFile := filepath.Join(featureFolder, "feature.tgz")
	registry := sanitizeURL(ref.Context().RegistryStr())
	err = downloadLayer(img, id, destFile)
	if err != nil {
		log.Debugf("failed to download feature layer: error=%v, featureId=%s", err, id)
		return nil, fmt.Errorf("download layer from %s: %w", registry, err)
	}

	file, err := os.Open(destFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	log.Debugf("extract feature: destination=%s", destDir)
	err = extract.Extract(file, destDir)
	if err != nil {
		log.Debugf("failed to extract feature: error=%v, destination=%s", err, destDir)
		_ = os.RemoveAll(destDir)
		return nil, err
	}

	return manifest.Annotations, nil
}

func validateImageManifest(img v1.Image) (*v1.Manifest, error) {
	manifest, err := img.Manifest()
	if err != nil {
		return nil, err
	}
	if manifest.Config.MediaType != DEVCONTAINER_MANIFEST_MEDIATYPE {
		return nil, fmt.Errorf(
			"incorrect manifest type %s, expected %s",
			manifest.Config.MediaType,
			DEVCONTAINER_MANIFEST_MEDIATYPE,
		)
	}
	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("unexpected amount of layers, expected at least 1")
	}
	return manifest, nil
}

func fetchLayerData(
	img v1.Image, manifest *v1.Manifest, id, destFile string,
) (io.ReadCloser, error) {
	log.Debugf(
		"download feature layer: featureId=%s, digest=%s, destFile=%s",
		id,
		manifest.Layers[0].Digest.String(),
		destFile,
	)
	layer, err := img.LayerByDigest(manifest.Layers[0].Digest)
	if err != nil {
		return nil, fmt.Errorf("retrieve layer: %w", err)
	}

	data, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}

	return data, nil
}

func writeLayerToFile(data io.Reader, destFile string) error {
	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err := os.MkdirAll(filepath.Dir(destFile), 0o755)
	if err != nil {
		return fmt.Errorf("create target folder: %w", err)
	}

	file, err := os.Create(destFile)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = file.Close() }()

	_, err = io.Copy(file, data)
	if err != nil {
		return fmt.Errorf("download layer: %w", err)
	}

	return nil
}

func downloadLayer(img v1.Image, id, destFile string) error {
	manifest, err := validateImageManifest(img)
	if err != nil {
		return err
	}

	data, err := fetchLayerData(img, manifest, id, destFile)
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	return writeLayerToFile(data, destFile)
}

// verifyCacheIntegrity checks a cached tarball against its stored
// SHA-256 sidecar. Returns true when the cache is safe to use.
func verifyCacheIntegrity(featureFolder, id string) bool {
	hashFile := filepath.Join(featureFolder, "feature.sha256")
	storedBytes, err := os.ReadFile(filepath.Clean(hashFile))
	if err != nil {
		log.Warnf(
			"No integrity hash for cached feature (backward compat): featureId=%s",
			id,
		)
		return true
	}

	tarball := filepath.Join(featureFolder, "feature.tgz")
	computed, err := hash.File(tarball)
	if err != nil {
		log.Errorf("Failed to hash cached tarball: error=%v, featureId=%s", err, id)
		return false
	}

	if computed != strings.TrimSpace(string(storedBytes)) {
		log.Errorf(
			"Integrity check failed for cached feature: featureId=%s",
			id,
		)
		return false
	}

	log.Debugf("Integrity check passed for cached feature: featureId=%s", id)
	return true
}

// storeIntegrityHash computes and persists the SHA-256 of a downloaded tarball.
func storeIntegrityHash(featureFolder, tarballPath, id string) {
	computed, err := hash.File(tarballPath)
	if err != nil {
		log.Errorf("Failed to compute tarball hash: error=%v, featureId=%s", err, id)
		return
	}

	hashFile := filepath.Join(featureFolder, "feature.sha256")
	if err := os.WriteFile(hashFile, []byte(computed), 0o600); err != nil {
		log.Errorf("Failed to write hash sidecar: error=%v, featureId=%s", err, id)
		return
	}

	log.Infof("Feature tarball integrity: featureId=%s, sha256=%s", id, computed)
}

func extractTarball(downloadFile, dest string) error {
	file, err := os.Open(filepath.Clean(downloadFile))
	if err != nil {
		return fmt.Errorf("open tarball: %w", err)
	}
	defer func() { _ = file.Close() }()

	if err := extract.Extract(file, dest); err != nil {
		_ = os.RemoveAll(dest)
		return fmt.Errorf("extract folder: %w", err)
	}

	return nil
}

func processDirectTarFeature(
	id string,
	httpHeaders map[string]string,
	forceDownload bool,
) (string, error) {
	log.Debugf("processing direct tar feature: featureId=%s, forceDownload=%v", id, forceDownload)

	downloadBase := id[strings.LastIndex(id, "/"):]
	if !directTarballRegEx.MatchString(downloadBase) {
		return "", fmt.Errorf(
			"expected tarball name to follow 'devcontainer-feature-<feature-id>.tgz' format.  Received '%s' ",
			downloadBase,
		)
	}

	featureFolder, err := getFeaturesTempFolder(id)
	if err != nil {
		return "", fmt.Errorf("resolve feature cache dir: %w", err)
	}

	featureExtractedFolder := filepath.Join(featureFolder, "extracted")

	// Check cache — verify integrity if present.
	_, statErr := os.Stat(featureExtractedFolder)
	if statErr == nil && !forceDownload {
		if verifyCacheIntegrity(featureFolder, id) {
			log.Debugf("direct tar feature already cached: folder=%s", featureExtractedFolder)
			return featureExtractedFolder, nil
		}
		_ = os.RemoveAll(featureFolder)
	}

	// Download feature tarball.
	downloadFile := filepath.Join(featureFolder, "feature.tgz")
	err = downloadFeatureFromURL(id, downloadFile, httpHeaders)
	if err != nil {
		log.Debugf("failed to download feature tarball: error=%v, url=%s", err, id)
		return "", err
	}

	storeIntegrityHash(featureFolder, downloadFile, id)

	if err := extractTarball(downloadFile, featureExtractedFolder); err != nil {
		log.Debugf("failed to extract tarball: error=%v, featureId=%s", err, id)
		return "", err
	}

	log.Infof(
		"Direct tar feature processed successfully: featureId=%s, path=%s",
		id,
		featureExtractedFolder,
	)
	return featureExtractedFolder, nil
}

func downloadFeatureFromURL(
	url string,
	destFile string,
	httpHeaders map[string]string,
) error {
	log.Debugf("starting feature download: url=%s, destFile=%s", url, destFile)

	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err := os.MkdirAll(filepath.Dir(destFile), 0o755)
	if err != nil {
		return fmt.Errorf("create feature folder: %w", err)
	}

	attempt := 0
	for range 3 {
		if attempt > 0 {
			delay := time.Duration(1<<uint(attempt-1)) * time.Second
			log.Debugf("retrying download: delay=%v, attempt=%v", delay, attempt)
			time.Sleep(delay)
		}

		log.Debugf("download feature: url=%s", url)
		if err := tryDownload(url, destFile, httpHeaders); err != nil {
			if attempt == 2 {
				return err
			}
			log.Debugf("download attempt failed: error=%v, attempt=%v", err, attempt)
			attempt++
			continue
		}
		log.Infof("Feature download completed successfully: url=%s, destFile=%s", url, destFile)
		return nil
	}

	return fmt.Errorf("download failed")
}

func tryDownload(url, destFile string, httpHeaders map[string]string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("make request: %w", err)
	}
	for key, value := range httpHeaders {
		req.Header.Set(key, value)
	}

	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("make request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("GET request failed, status code is %d", resp.StatusCode)
	}

	file, err := os.Create(destFile)
	if err != nil {
		return fmt.Errorf("create download file: %w", err)
	}
	defer func() { _ = file.Close() }()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("download feature: %w", err)
	}

	return nil
}

func getFeaturesTempFolder(id string) (string, error) {
	hashedID := hash.String(id)[:10]

	return pkgconfig.DefaultPathManager().FeatureCacheDir(hashedID)
}
