package features

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	ginkgo.Describe("features resolve-dependencies", func() {
		ginkgo.It("outputs install order for features", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			workspaceDir, err := os.MkdirTemp("", "e2e-resolve-deps-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(workspaceDir) })

			devcontainerDir := workspaceDir + "/.devcontainer"
			framework.ExpectNoError(os.MkdirAll(devcontainerDir, 0o750))

			goFeatureDir := devcontainerDir + "/local-features/go"
			framework.ExpectNoError(os.MkdirAll(goFeatureDir, 0o750))
			framework.ExpectNoError(os.WriteFile(
				goFeatureDir+"/devcontainer-feature.json",
				[]byte(`{"id":"go","version":"1.0.0","name":"Go"}`),
				0o600,
			))

			nodeFeatureDir := devcontainerDir + "/local-features/node"
			framework.ExpectNoError(os.MkdirAll(nodeFeatureDir, 0o750))
			framework.ExpectNoError(os.WriteFile(
				nodeFeatureDir+"/devcontainer-feature.json",
				[]byte(`{"id":"node","version":"1.0.0","name":"Node.js"}`),
				0o600,
			))

			devcontainerJSON := `{
				"image": "ubuntu:22.04",
				"features": {
					"./local-features/go": {
						"version": "1.21"
					},
					"./local-features/node": {}
				}
			}`
			framework.ExpectNoError(os.WriteFile(
				devcontainerDir+"/devcontainer.json",
				[]byte(devcontainerJSON),
				0o600,
			))

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "resolve-dependencies",
				"--workspace-folder", workspaceDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("Feature install order"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("outputs JSON when --output=json is specified", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			workspaceDir, err := os.MkdirTemp("", "e2e-resolve-deps-json-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(workspaceDir) })

			devcontainerDir := workspaceDir + "/.devcontainer"
			framework.ExpectNoError(os.MkdirAll(devcontainerDir, 0o750))

			goFeatureDir := devcontainerDir + "/local-features/go"
			framework.ExpectNoError(os.MkdirAll(goFeatureDir, 0o750))
			framework.ExpectNoError(os.WriteFile(
				goFeatureDir+"/devcontainer-feature.json",
				[]byte(`{"id":"go","version":"1.0.0","name":"Go"}`),
				0o600,
			))

			devcontainerJSON := `{
				"image": "ubuntu:22.04",
				"features": {
					"./local-features/go": {}
				}
			}`
			framework.ExpectNoError(os.WriteFile(
				devcontainerDir+"/devcontainer.json",
				[]byte(devcontainerJSON),
				0o600,
			))

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "resolve-dependencies",
				"--workspace-folder", workspaceDir,
				"--output", "json",
			})
			framework.ExpectNoError(err)

			var result []map[string]any
			gomega.Expect(json.Unmarshal([]byte(stdout), &result)).To(gomega.Succeed())
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("rejects invalid output format", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			workspaceDir, err := os.MkdirTemp("", "e2e-resolve-deps-invalid-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(workspaceDir) })

			_, stderr, err := f.ExecCommandCapture(ctx, []string{
				"features", "resolve-dependencies",
				"--workspace-folder", workspaceDir,
				"--output", "yaml",
			})
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(stderr).To(gomega.ContainSubstring("invalid output format"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
	})

	ginkgo.Describe("features generate-docs", func() {
		ginkgo.It("generates markdown files from feature metadata", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			projectDir, err := os.MkdirTemp("", "e2e-generate-docs-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(projectDir) })

			srcDir := projectDir + "/src/my-feature"
			framework.ExpectNoError(os.MkdirAll(srcDir, 0o750))

			featureJSON := `{
				"id": "my-feature",
				"version": "1.0.0",
				"name": "My Feature",
				"description": "A test feature for E2E"
			}`
			framework.ExpectNoError(os.WriteFile(
				srcDir+"/devcontainer-feature.json",
				[]byte(featureJSON),
				0o600,
			))

			outputDir, err := os.MkdirTemp("", "e2e-generate-docs-output-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(outputDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "generate-docs",
				"--project-folder", projectDir,
				"--output-folder", outputDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("Generated:"))

			docContent, err := os.ReadFile(
				filepath.Clean(filepath.Join(outputDir, "my-feature.md")),
			)
			framework.ExpectNoError(err)
			gomega.Expect(string(docContent)).To(gomega.ContainSubstring("# My Feature"))
			gomega.Expect(string(docContent)).To(gomega.ContainSubstring("A test feature for E2E"))

			_, err = os.Stat(filepath.Join(outputDir, "README.md"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("generates docs with namespace linking", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			projectDir, err := os.MkdirTemp("", "e2e-generate-docs-ns-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(projectDir) })

			srcDir := projectDir + "/src/go"
			framework.ExpectNoError(os.MkdirAll(srcDir, 0o750))

			featureJSON := `{
				"id": "go",
				"version": "1.0.0",
				"name": "Go",
				"description": "Install Go toolchain"
			}`
			framework.ExpectNoError(os.WriteFile(
				srcDir+"/devcontainer-feature.json",
				[]byte(featureJSON),
				0o600,
			))

			outputDir, err := os.MkdirTemp("", "e2e-generate-docs-ns-output-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(outputDir) })

			_, _, err = f.ExecCommandCapture(ctx, []string{
				"features", "generate-docs",
				"--project-folder", projectDir,
				"--output-folder", outputDir,
				"--namespace", "ghcr.io/test/features",
			})
			framework.ExpectNoError(err)

			docContent, err := os.ReadFile(filepath.Clean(filepath.Join(outputDir, "go.md")))
			framework.ExpectNoError(err)
			gomega.Expect(string(docContent)).To(gomega.ContainSubstring("ghcr.io/test/features"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
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

	ginkgo.Describe("features test", func() {
		ginkgo.It("tests a feature with a passing test script", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			featureDir, err := os.MkdirTemp("", "e2e-features-test-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(featureDir) })

			// Create feature metadata
			featureJSON := `{
				"id": "hello",
				"version": "1.0.0",
				"name": "Hello Feature",
				"description": "A simple feature that creates a hello script"
			}`
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(featureDir, "devcontainer-feature.json"),
				[]byte(featureJSON),
				0o600,
			))

			// Create install script
			installScript := "#!/bin/sh\nset -e\n" +
				"echo 'hello-feature' > /usr/local/bin/hello-feature\n" +
				"chmod +x /usr/local/bin/hello-feature\n"
			framework.ExpectNoError(os.WriteFile( //nolint:gosec // test needs exec permission
				filepath.Join(featureDir, "install.sh"),
				[]byte(installScript),
				0o755,
			))

			// Create test directory with a passing test
			testDir := filepath.Join(featureDir, "test")
			framework.ExpectNoError(os.MkdirAll(testDir, 0o750))
			testScript := "#!/bin/bash\nset -e\n" +
				"test -f /usr/local/bin/hello-feature\n"
			framework.ExpectNoError(os.WriteFile( //nolint:gosec // test needs exec permission
				filepath.Join(testDir, "test_hello.sh"),
				[]byte(testScript),
				0o755,
			))

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "test",
				"--project-folder", featureDir,
				"--base-image", "ubuntu:22.04",
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("PASS"))
			gomega.Expect(stdout).To(gomega.ContainSubstring("hello"))
		}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

		ginkgo.It("reports failure for a failing test script", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			featureDir, err := os.MkdirTemp("", "e2e-features-test-fail-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(featureDir) })

			featureJSON := `{
				"id": "failing",
				"version": "1.0.0",
				"name": "Failing Feature"
			}`
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(featureDir, "devcontainer-feature.json"),
				[]byte(featureJSON),
				0o600,
			))

			installScript := "#!/bin/sh\nset -e\necho installed\n"
			framework.ExpectNoError(os.WriteFile( //nolint:gosec // test needs exec permission
				filepath.Join(featureDir, "install.sh"),
				[]byte(installScript),
				0o755,
			))

			testDir := filepath.Join(featureDir, "test")
			framework.ExpectNoError(os.MkdirAll(testDir, 0o750))
			testScript := "#!/bin/bash\nexit 1\n"
			framework.ExpectNoError(os.WriteFile( //nolint:gosec // test needs exec permission
				filepath.Join(testDir, "test_should_fail.sh"),
				[]byte(testScript),
				0o755,
			))

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "test",
				"--project-folder", featureDir,
				"--base-image", "ubuntu:22.04",
			})
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(stdout).To(gomega.ContainSubstring("FAIL"))
		}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

		ginkgo.It("outputs JSON when --output=json is specified", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			featureDir, err := os.MkdirTemp("", "e2e-features-test-json-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(featureDir) })

			featureJSON := `{
				"id": "json-test",
				"version": "1.0.0",
				"name": "JSON Test Feature"
			}`
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(featureDir, "devcontainer-feature.json"),
				[]byte(featureJSON),
				0o600,
			))

			installScript := "#!/bin/sh\necho ok\n"
			framework.ExpectNoError(os.WriteFile( //nolint:gosec // test needs exec permission
				filepath.Join(featureDir, "install.sh"),
				[]byte(installScript),
				0o755,
			))

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "test",
				"--project-folder", featureDir,
				"--base-image", "ubuntu:22.04",
				"--skip-scenarios",
				"--output", "json",
			})
			framework.ExpectNoError(err)

			var result map[string]any
			gomega.Expect(json.Unmarshal([]byte(stdout), &result)).To(gomega.Succeed())
			gomega.Expect(result["featureId"]).To(gomega.Equal("json-test"))
			gomega.Expect(result["passed"]).To(gomega.BeTrue())
		}, ginkgo.SpecTimeout(framework.TimeoutModerate()))
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
