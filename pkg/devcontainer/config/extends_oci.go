package config

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
// (e.g. "ghcr.io/owner/repo:tag") rather than a local file path.
func isOCIRef(ref string) bool {
	if strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/") {
		return false
	}
	if strings.HasSuffix(ref, ".json") || strings.HasSuffix(ref, ".jsonc") {
		return false
	}
	return strings.Contains(ref, "/")
}

// resolveOCIExtends fetches a devcontainer.json from an OCI artifact and
// recursively resolves any extends within it.
func resolveOCIExtends(
	ociRef string,
	visited map[string]bool,
) (*DevContainerConfig, error) {
	if visited[ociRef] {
		return nil, fmt.Errorf("extends: cycle detected, OCI ref %q already in chain", ociRef)
	}
	visited[ociRef] = true

	data, err := pullOCIExtendsJSON(ociRef)
	if err != nil {
		return nil, fmt.Errorf("extends: fetch OCI %q: %w", ociRef, err)
	}

	devContainer := &DevContainerConfig{}
	normalized, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("extends: parse jsonc from OCI %q: %w", ociRef, err)
	}
	if err := json.Unmarshal(normalized, devContainer); err != nil {
		return nil, fmt.Errorf("extends: unmarshal OCI %q: %w", ociRef, err)
	}
	devContainer.Origin = "oci://" + ociRef

	if !devContainer.Extends.IsEmpty() {
		parent, err := resolveExtendsArray(devContainer.Extends, "", visited)
		if err != nil {
			return nil, err
		}
		devContainer = mergeExtendsConfigs(parent, devContainer)
	}

	return devContainer, nil
}

// pullOCIExtendsJSON fetches an OCI image and extracts devcontainer.json
// from its first layer (expected to be a gzipped tarball).
func pullOCIExtendsJSON(ociRef string) ([]byte, error) {
	ref, err := name.ParseReference(ociRef)
	if err != nil {
		return nil, fmt.Errorf("parse reference: %w", err)
	}

	var img v1.Image
	err = retryOCIExtendsPull(func() error {
		var fetchErr error
		img, fetchErr = remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
		return fetchErr
	})
	if err != nil {
		return nil, fmt.Errorf("pull image: %w", err)
	}

	return extractDevContainerJSON(img)
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
