package features

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("features commands", ginkgo.Label("features"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.Describe("features info", func() {
		ginkgo.It("displays feature metadata from a registry", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureRef := regHost + "/test/features/go:1.0.0"

			pushFeatureWithAnnotations(featureRef, map[string]string{
				"org.opencontainers.image.title":       "Go",
				"org.opencontainers.image.description": "Install Go toolchain",
				"org.opencontainers.image.version":     "1.0.0",
				"org.opencontainers.image.source":      "https://github.com/test/features",
			})

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "info", featureRef,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("Go"))
			gomega.Expect(stdout).To(gomega.ContainSubstring("1.0.0"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("outputs JSON when --output=json is specified", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureRef := regHost + "/test/features/node:1.0.0"

			pushFeatureWithAnnotations(featureRef, map[string]string{
				"org.opencontainers.image.title":   "Node.js",
				"org.opencontainers.image.version": "1.0.0",
			})

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "info", featureRef, "--output", "json",
			})
			framework.ExpectNoError(err)

			var result map[string]any
			gomega.Expect(json.Unmarshal([]byte(stdout), &result)).To(gomega.Succeed())
			gomega.Expect(result["id"]).To(gomega.Equal("node"))
			gomega.Expect(result["version"]).To(gomega.Equal("1.0.0"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("lists tags with --show-tags", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureRepo := regHost + "/test/features/python"

			pushFeatureWithAnnotations(featureRepo+":1.0.0", nil)
			pushFeatureWithAnnotations(featureRepo+":2.0.0", nil)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "info", featureRepo + ":1.0.0", "--show-tags",
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("1.0.0"))
			gomega.Expect(stdout).To(gomega.ContainSubstring("2.0.0"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
	})
})

func pushFeatureWithAnnotations(refStr string, annotations map[string]string) {
	featureJSON := `{"id":"` + featureIDFromRef(refStr) + `","name":"` +
		annotationOrDefault(annotations, "org.opencontainers.image.title", "Test Feature") +
		`","version":"1.0.0","description":"` +
		annotationOrDefault(
			annotations,
			"org.opencontainers.image.description",
			"A test feature",
		) + `"}`

	layer := static.NewLayer(
		buildFeatureTarGz("devcontainer-feature.json", featureJSON),
		types.OCILayer,
	)

	img, err := mutate.AppendLayers(empty.Image, layer)
	framework.ExpectNoError(err)

	img = mutate.MediaType(img, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, "application/vnd.devcontainers")

	if annotations != nil {
		img = mutate.Annotations(img, annotations).(v1.Image)
	}

	ref, err := name.ParseReference(refStr, name.Insecure)
	framework.ExpectNoError(err)

	framework.ExpectNoError(remote.Write(ref, img))
}

func featureIDFromRef(refStr string) string {
	parts := strings.Split(refStr, "/")
	last := parts[len(parts)-1]
	if id, _, found := strings.Cut(last, ":"); found {
		return id
	}
	return last
}

func annotationOrDefault(annotations map[string]string, key, defaultVal string) string {
	if annotations == nil {
		return defaultVal
	}
	if v, ok := annotations[key]; ok {
		return v
	}
	return defaultVal
}

func buildFeatureTarGz(filename, content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: filename,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	gomega.Expect(tw.WriteHeader(hdr)).To(gomega.Succeed())
	_, err := tw.Write([]byte(content))
	framework.ExpectNoError(err)
	gomega.Expect(tw.Close()).To(gomega.Succeed())
	gomega.Expect(gz.Close()).To(gomega.Succeed())
	return buf.Bytes()
}
