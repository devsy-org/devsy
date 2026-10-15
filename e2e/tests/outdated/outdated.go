package outdated

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	cmdFeature          = "feature"
	cmdOutdated         = "outdated"
	flagWorkspaceFolder = "--workspace-folder"
)

var _ = ginkgo.Describe("outdated command", ginkgo.Label("outdated"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("reports newer versions available for a feature", func(ctx context.Context) {
		f := framework.NewDefaultFramework(initialDir + "/bin")

		srv := httptest.NewServer(registry.New())
		ginkgo.DeferCleanup(func() { srv.Close() })

		regHost := strings.TrimPrefix(srv.URL, "http://")
		featureRepo := regHost + "/test/my-feature"

		framework.PushFeatureTag(featureRepo + ":1.0.0")
		framework.PushFeatureTag(featureRepo + ":1.1.0")
		framework.PushFeatureTag(featureRepo + ":2.0.0")

		tempDir := framework.WriteTempDevcontainer("outdated-test-*", fmt.Sprintf(`{
			"image": "ubuntu:22.04",
			"features": {"%s:1.0.0": {}}
		}`, featureRepo))
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

		stdout, _, err := f.ExecCommandCapture(ctx, []string{
			cmdFeature, cmdOutdated,
			flagWorkspaceFolder, tempDir,
		})
		framework.ExpectNoError(err)

		gomega.Expect(stdout).To(gomega.ContainSubstring("2.0.0"))
		gomega.Expect(stdout).To(gomega.ContainSubstring("1.0.0"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It(
		"reports all up to date when feature is at latest version",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureRepo := regHost + "/test/my-feature"

			framework.PushFeatureTag(featureRepo + ":1.0.0")
			framework.PushFeatureTag(featureRepo + ":1.1.0")

			tempDir := framework.WriteTempDevcontainer("outdated-test-*", fmt.Sprintf(`{
			"image": "ubuntu:22.04",
			"features": {"%s:1.1.0": {}}
		}`, featureRepo))
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeature, cmdOutdated,
				flagWorkspaceFolder, tempDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("All features are up to date."))
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"reports no features found when devcontainer has no features",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			tempDir := framework.WriteTempDevcontainer("outdated-test-*", `{
			"image": "ubuntu:22.04"
		}`)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeature, cmdOutdated,
				flagWorkspaceFolder, tempDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("No features found."))
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)
})
