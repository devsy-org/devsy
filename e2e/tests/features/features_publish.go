package features

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	cmdPublish       = "publish"
	flagRegistry     = "--registry"
	flagNamespace    = "--namespace"
	fileFeatureJSON  = "devcontainer-feature.json"
	fileInstallShell = "install.sh"
)

var _ = ginkgo.Describe("features publish", ginkgo.Label("features"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("publishes a feature to an OCI registry", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		srv := httptest.NewServer(registry.New())
		ginkgo.DeferCleanup(func() { srv.Close() })

		regHost := strings.TrimPrefix(srv.URL, "http://")

		featureDir := createFeatureDir("go", "1.0.0", "Go")

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			cmdFeatures, cmdPublish,
			flagTarget, featureDir,
			flagRegistry, regHost,
			flagNamespace, "test/features",
		})
		framework.ExpectNoError(err)

		var result map[string]any
		gomega.Expect(json.Unmarshal([]byte(stdout), &result)).To(gomega.Succeed())
		gomega.Expect(result["id"]).To(gomega.Equal("go"))
		gomega.Expect(result["version"]).To(gomega.Equal("1.0.0"))
		gomega.Expect(result["ref"]).
			To(gomega.ContainSubstring(regHost + "/test/features/go:1.0.0"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("publishes without namespace", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		srv := httptest.NewServer(registry.New())
		ginkgo.DeferCleanup(func() { srv.Close() })

		regHost := strings.TrimPrefix(srv.URL, "http://")

		featureDir := createFeatureDir("node", "2.0.0", "Node.js")

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			cmdFeatures, cmdPublish,
			flagTarget, featureDir,
			flagRegistry, regHost,
		})
		framework.ExpectNoError(err)

		var result map[string]any
		gomega.Expect(json.Unmarshal([]byte(stdout), &result)).To(gomega.Succeed())
		gomega.Expect(result["id"]).To(gomega.Equal("node"))
		gomega.Expect(result["version"]).To(gomega.Equal("2.0.0"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("uses latest when version is empty", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		srv := httptest.NewServer(registry.New())
		ginkgo.DeferCleanup(func() { srv.Close() })

		regHost := strings.TrimPrefix(srv.URL, "http://")

		featureDir, err := os.MkdirTemp("", "e2e-publish-noversion-*")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(featureDir) })

		framework.ExpectNoError(os.WriteFile(
			filepath.Join(featureDir, fileFeatureJSON),
			[]byte(`{"id": "python", "name": "Python"}`),
			0o600,
		))

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			cmdFeatures, cmdPublish,
			flagTarget, featureDir,
			flagRegistry, regHost,
			flagNamespace, "test/features",
		})
		framework.ExpectNoError(err)

		var result map[string]any
		gomega.Expect(json.Unmarshal([]byte(stdout), &result)).To(gomega.Succeed())
		gomega.Expect(result["version"]).To(gomega.Equal("latest"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("fails when target directory does not exist", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		_, _, err := f.ExecCommandCapture(ctx, []string{
			cmdFeatures, cmdPublish,
			flagTarget, "/nonexistent/path",
		})
		gomega.Expect(err).To(gomega.HaveOccurred())
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("fails when target has no feature metadata", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		emptyDir, err := os.MkdirTemp("", "e2e-publish-empty-*")
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(emptyDir) })

		_, _, err = f.ExecCommandCapture(ctx, []string{
			cmdFeatures, cmdPublish,
			flagTarget, emptyDir,
		})
		gomega.Expect(err).To(gomega.HaveOccurred())
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})

func createFeatureDir(id, version, displayName string) string {
	dir, err := os.MkdirTemp("", "e2e-publish-feature-*")
	framework.ExpectNoError(err)

	featureJSON := `{"id": "` + id + `", "version": "` + version + `", "name": "` + displayName + `"}`
	framework.ExpectNoError(os.WriteFile(
		filepath.Join(dir, fileFeatureJSON),
		[]byte(featureJSON),
		0o600,
	))

	// #nosec G306 -- test install script must be executable
	framework.ExpectNoError(os.WriteFile(
		filepath.Join(dir, fileInstallShell),
		[]byte("#!/bin/bash\necho installed\n"),
		0o750,
	))

	return dir
}
