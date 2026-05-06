package config

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/tailscale/hujson"
	"k8s.io/apimachinery/pkg/util/wait"
)

var ociExtendsBackoff = wait.Backoff{
	Duration: 1 * time.Second,
	Factor:   2.0,
	Steps:    3,
}

// isOCIRef returns true if the extends reference looks like an OCI image ref
// (e.g. "ghcr.io/owner/repo:tag" or "oci://ghcr.io/owner/repo:tag") rather than a local file path.
func isOCIRef(ref string) bool {
	if strings.HasPrefix(ref, "oci://") {
		return true
	}
	if strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/") {
		return false
	}
	if strings.HasSuffix(ref, ".json") || strings.HasSuffix(ref, ".jsonc") {
		return false
	}
	return strings.Contains(ref, "/")
}

// stripOCIPrefix removes the "oci://" prefix from an OCI reference if present.
func stripOCIPrefix(ref string) string {
	return strings.TrimPrefix(ref, "oci://")
}

// resolveOCIExtends fetches a devcontainer.json from an OCI artifact and
// recursively resolves any extends within it.
func resolveOCIExtends(
	ctx context.Context,
	ociRef string,
	visited map[string]bool,
) (*DevContainerConfig, error) {
	bare := stripOCIPrefix(ociRef)
	if visited[bare] {
		return nil, fmt.Errorf("extends: cycle detected, OCI ref %q already in chain", bare)
	}
	visited[bare] = true

	data, err := pullOCIExtendsJSON(ctx, bare)
	if err != nil {
		return nil, fmt.Errorf("extends: fetch OCI %q: %w", bare, err)
	}

	devContainer := &DevContainerConfig{}
	normalized, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("extends: parse jsonc from OCI %q: %w", bare, err)
	}
	if err := json.Unmarshal(normalized, devContainer); err != nil {
		return nil, fmt.Errorf("extends: unmarshal OCI %q: %w", bare, err)
	}
	devContainer.Origin = "oci://" + bare

	if !devContainer.Extends.IsEmpty() {
		parent, err := resolveExtendsArray(ctx, devContainer.Extends, "", visited)
		if err != nil {
			return nil, err
		}
		devContainer = mergeExtendsConfigs(parent, devContainer)
	}

	return devContainer, nil
}

// pullOCIExtendsJSON fetches an OCI image and extracts devcontainer.json
// from its first layer (expected to be a gzipped tarball). Uses digest-based
// caching to avoid repeated pulls.
func pullOCIExtendsJSON(ctx context.Context, ociRef string) ([]byte, error) {
	ref, err := name.ParseReference(ociRef)
	if err != nil {
		return nil, fmt.Errorf("parse reference: %w", err)
	}

	kc := getKeychain(ctx)

	cacheDir, cacheErr := extendsCacheDir(ociRef)
	if cacheErr == nil {
		if data, ok := checkExtendsCache(cacheDir, ref, kc); ok {
			return data, nil
		}
	}

	var img v1.Image
	err = retryOCIExtendsPull(func() error {
		var fetchErr error
		img, fetchErr = remote.Image(ref, remote.WithAuthFromKeychain(kc))
		return fetchErr
	})
	if err != nil {
		return nil, fmt.Errorf("pull image: %w", err)
	}

	data, err := extractDevContainerJSON(img)
	if err != nil {
		return nil, err
	}

	if cacheErr == nil {
		writeExtendsCache(cacheDir, ref, kc, data)
	}

	return data, nil
}

func getKeychain(ctx context.Context) authn.Keychain {
	kc, err := image.GetKeychain(ctx)
	if err != nil {
		return authn.DefaultKeychain
	}
	return kc
}

func extendsCacheDir(ociRef string) (string, error) {
	h := sha256.Sum256([]byte(ociRef))
	hashed := hex.EncodeToString(h[:])

	base, err := pkgconfig.DefaultPathManager().CacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "extends", hashed)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func checkExtendsCache(cacheDir string, ref name.Reference, kc authn.Keychain) ([]byte, bool) {
	jsonPath := filepath.Join(cacheDir, "devcontainer.json")
	digestPath := filepath.Join(cacheDir, "digest")

	// #nosec G304 -- paths derived from our own cache directory, not user input
	storedDigest, err := os.ReadFile(digestPath)
	if err != nil {
		return nil, false
	}
	// #nosec G304 -- paths derived from our own cache directory, not user input
	cachedJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, false
	}

	desc, err := remote.Head(ref, remote.WithAuthFromKeychain(kc))
	if err != nil {
		return nil, false
	}

	if desc.Digest.String() == strings.TrimSpace(string(storedDigest)) {
		return cachedJSON, true
	}
	return nil, false
}

func writeExtendsCache(cacheDir string, ref name.Reference, kc authn.Keychain, data []byte) {
	desc, err := remote.Head(ref, remote.WithAuthFromKeychain(kc))
	if err != nil {
		return
	}

	jsonPath := filepath.Join(cacheDir, "devcontainer.json")
	digestPath := filepath.Join(cacheDir, "digest")
	_ = os.WriteFile(jsonPath, data, 0o600)
	_ = os.WriteFile(digestPath, []byte(desc.Digest.String()), 0o600)
}

// extractDevContainerJSON reads the first layer of an OCI image as a
// gzipped tarball and returns the contents of devcontainer.json.
func extractDevContainerJSON(img v1.Image) ([]byte, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("get layers: %w", err)
	}
	if len(layers) == 0 {
		return nil, errors.New("OCI image has no layers")
	}

	rc, err := layers[0].Compressed()
	if err != nil {
		return nil, fmt.Errorf("read layer: %w", err)
	}
	defer func() { _ = rc.Close() }()

	return findDevContainerInGzip(rc)
}

func findDevContainerInGzip(rc io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(rc)
	if err != nil {
		return nil, fmt.Errorf("decompress layer: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		base := strings.TrimPrefix(hdr.Name, "./")
		if base == "devcontainer.json" || base == ".devcontainer.json" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read devcontainer.json from tar: %w", err)
			}
			return data, nil
		}
	}

	return nil, errors.New("devcontainer.json not found in OCI layer")
}

func retryOCIExtendsPull(fn func() error) error {
	var lastErr error
	err := wait.ExponentialBackoff(ociExtendsBackoff, func() (bool, error) {
		lastErr = fn()
		if lastErr == nil {
			return true, nil
		}
		if !isOCIExtendsTransientError(lastErr) {
			return false, lastErr
		}
		return false, nil
	})
	if wait.Interrupted(err) {
		return lastErr
	}
	return err
}

func isOCIExtendsTransientError(err error) bool {
	if err == nil {
		return false
	}
	var terr *transport.Error
	if errors.As(err, &terr) {
		return terr.StatusCode >= http.StatusInternalServerError
	}
	return true
}
