//go:build windows

package up

import (
	"context"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega/ghttp"
)

var _ = ginkgo.Describe(
	"devsy up docker features test suite",
	ginkgo.Label("up-features", "wsl"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should use http headers to download feature", func(ctx context.Context) {
			server := ghttp.NewServer()
			ginkgo.DeferCleanup(server.Close)

			tempDir, err := framework.CopyToTempDir(
				"tests/up-features/testdata/docker-features-http-headers",
			)
			framework.ExpectNoError(err)

			featureArchiveFilePath := path.Join(tempDir, "devcontainer-feature-hello.tgz")
			featureFiles := []string{
				path.Join(tempDir, "devcontainer-feature.json"),
				path.Join(tempDir, "install.sh"),
			}
			err = createTarGzArchive(featureArchiveFilePath, featureFiles)
			framework.ExpectNoError(err)

			devContainerFileBuf, err := os.ReadFile(path.Join(tempDir, ".devcontainer.json"))
			framework.ExpectNoError(err)

			output := strings.ReplaceAll(string(devContainerFileBuf), "#{server_url}", server.URL())
			err = os.WriteFile(path.Join(tempDir, ".devcontainer.json"), []byte(output), 0o644)
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			respHeader := http.Header{}
			respHeader.Set(
				"Content-Disposition",
				"attachment; filename=devcontainer-feature-hello.tgz",
			)

			featureArchiveFileBuf, err := os.ReadFile(featureArchiveFilePath)
			framework.ExpectNoError(err)

			f := framework.NewDefaultFramework(initialDir + "/bin")
			server.AppendHandlers(
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/devcontainer-feature-hello.tgz"),
					ghttp.VerifyHeaderKV("Foo-Header", "Foo"),
					ghttp.RespondWith(http.StatusOK, featureArchiveFileBuf, respHeader),
				),
			)

			_ = f.DevsyProviderDelete(ctx, "docker")
			err = f.DevsyProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevsyProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			// Wait for devsy workspace to come online (deadline: 30s)
			err = f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutLong()))

		ginkgo.It(
			"ensure dependencies installed via features are accessible in lifecycle hooks",
			func(ctx context.Context) {
				f, err := setupDockerProvider(initialDir+"/bin", "docker")
				framework.ExpectNoError(err)

				tempDir, err := framework.CopyToTempDir(
					"tests/up-features/testdata/docker-features-lifecycle-hooks",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				workspaceName := filepath.Base(tempDir)
				ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, workspaceName)

				// Wait for devsy workspace to come online (deadline: 30s)
				err = f.DevsyUp(ctx, tempDir, "--debug")
				framework.ExpectNoError(err)
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)
	},
)
