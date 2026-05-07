package config

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/fake"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func TestIsOCIRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"ghcr.io/owner/repo:tag", true},
		{"docker.io/library/ubuntu:latest", true},
		{"myregistry.com/org/devcontainer-base:1", true},
		{"oci://ghcr.io/org/repo:tag", true},
		{"oci://relative/path", true},
		{"./base.json", false},
		{"../shared/base.json", false},
		{"/absolute/path.json", false},
		{"base.json", false},
		{"relative/path.json", false},
		{"relative/path.jsonc", false},
	}
	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			got := isOCIRef(tc.ref)
			if got != tc.want {
				t.Errorf("isOCIRef(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

func TestExtractDevContainerJSON(t *testing.T) {
	content := `{"name": "from-oci", "image": "ubuntu:22.04"}`
	img := createFakeImageWithJSON(t, "devcontainer.json", content)

	data, err := extractDevContainerJSON(img)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("got %q, want %q", string(data), content)
	}
}

func TestExtractDevContainerJSON_PrefixedPath(t *testing.T) {
	content := `{"name": "prefixed"}`
	img := createFakeImageWithJSON(t, "./devcontainer.json", content)

	data, err := extractDevContainerJSON(img)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("got %q, want %q", string(data), content)
	}
}

func TestExtractDevContainerJSON_NotFound(t *testing.T) {
	img := createFakeImageWithJSON(t, "other-file.txt", "hello")

	_, err := extractDevContainerJSON(img)
	if err == nil {
		t.Fatal("expected error for missing devcontainer.json")
	}
}

func TestResolveOCIExtends_Integration(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()

	regHost := strings.TrimPrefix(srv.URL, "http://")

	jsonContent := `{
		"name": "oci-parent",
		"image": "ubuntu:22.04",
		"remoteUser": "vscode",
		"containerEnv": {"FROM_OCI": "oci-value"}
	}`

	pushTestImage(t, regHost+"/test/devcontainer-base:latest", jsonContent)

	visited := map[string]bool{}
	cfg, err := resolveOCIExtends(
		context.Background(),
		regHost+"/test/devcontainer-base:latest",
		visited,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "oci-parent" {
		t.Errorf("Name: got %q, want 'oci-parent'", cfg.Name)
	}
	if cfg.Image != "ubuntu:22.04" {
		t.Errorf("Image: got %q, want 'ubuntu:22.04'", cfg.Image)
	}
	if cfg.RemoteUser != testUserVscode {
		t.Errorf("RemoteUser: got %q, want %q", cfg.RemoteUser, testUserVscode)
	}
	if cfg.ContainerEnv["FROM_OCI"] != "oci-value" {
		t.Error("missing FROM_OCI env var")
	}
}

func TestResolveOCIExtends_OCIPrefix(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()

	regHost := strings.TrimPrefix(srv.URL, "http://")

	jsonContent := `{"name": "oci-prefix-test", "image": "node:20"}`
	pushTestImage(t, regHost+"/org/config:v1", jsonContent)

	visited := map[string]bool{}
	cfg, err := resolveOCIExtends(context.Background(), "oci://"+regHost+"/org/config:v1", visited)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "oci-prefix-test" {
		t.Errorf("Name: got %q, want 'oci-prefix-test'", cfg.Name)
	}
	if cfg.Image != "node:20" {
		t.Errorf("Image: got %q, want 'node:20'", cfg.Image)
	}
}

func TestResolveOCIExtends_CycleDetection(t *testing.T) {
	ref := "ghcr.io/fake/cycle:1"
	visited := map[string]bool{ref: true}

	_, err := resolveOCIExtends(context.Background(), ref, visited)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected 'cycle' in error, got: %v", err)
	}
}

func TestResolveOCIExtends_CacheHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := httptest.NewServer(registry.New())
	defer srv.Close()

	regHost := strings.TrimPrefix(srv.URL, "http://")
	ref := regHost + "/test/cache-hit:latest"

	pushTestImage(t, ref, `{"name": "cached", "image": "alpine:3"}`)

	visited := map[string]bool{}
	cfg, err := resolveOCIExtends(context.Background(), ref, visited)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "cached" {
		t.Fatalf("first resolve: got name %q", cfg.Name)
	}

	cacheDir, err := extendsCacheDir(ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "devcontainer.json")); err != nil {
		t.Fatal("cache file not written")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "digest")); err != nil {
		t.Fatal("digest file not written")
	}

	visited2 := map[string]bool{}
	cfg2, err := resolveOCIExtends(context.Background(), ref, visited2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Name != "cached" {
		t.Errorf("cache hit: got name %q, want 'cached'", cfg2.Name)
	}
}

func TestResolveOCIExtends_CacheInvalidation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := httptest.NewServer(registry.New())
	defer srv.Close()

	regHost := strings.TrimPrefix(srv.URL, "http://")
	ref := regHost + "/test/cache-invalidate:latest"

	pushTestImage(t, ref, `{"name": "version1", "image": "alpine:3"}`)

	visited := map[string]bool{}
	cfg, err := resolveOCIExtends(context.Background(), ref, visited)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "version1" {
		t.Fatalf("first resolve: got name %q", cfg.Name)
	}

	pushTestImage(t, ref, `{"name": "version2", "image": "alpine:3.18"}`)

	visited2 := map[string]bool{}
	cfg2, err := resolveOCIExtends(context.Background(), ref, visited2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Name != "version2" {
		t.Errorf("after invalidation: got name %q, want 'version2'", cfg2.Name)
	}
}

func pushTestImage(t *testing.T, refStr, jsonContent string) {
	t.Helper()

	layer := static.NewLayer(
		buildTarGz(t, "devcontainer.json", jsonContent),
		types.OCILayer,
	)

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := name.ParseReference(refStr, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}

	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
}

func buildTarGz(t *testing.T, filename, content string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: filename,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func createFakeImageWithJSON(t *testing.T, filename, content string) v1.Image {
	t.Helper()

	layer := static.NewLayer(buildTarGz(t, filename, content), types.OCILayer)

	return &fake.FakeImage{
		LayersStub: func() ([]v1.Layer, error) {
			return []v1.Layer{layer}, nil
		},
	}
}
