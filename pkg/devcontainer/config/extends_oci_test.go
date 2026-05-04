package config

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http/httptest"
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
	cfg, err := resolveOCIExtends(regHost+"/test/devcontainer-base:latest", visited)
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

func TestResolveOCIExtends_CycleDetection(t *testing.T) {
	ref := "ghcr.io/fake/cycle:1"
	visited := map[string]bool{ref: true}

	_, err := resolveOCIExtends(ref, visited)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected 'cycle' in error, got: %v", err)
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
