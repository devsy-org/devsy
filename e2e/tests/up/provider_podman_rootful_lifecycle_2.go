package up

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for podman provider",
	ginkgo.Label("up-provider-podman-rootful-lifecycle-2"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.Context("with rootful podman", func() {
			var f *framework.Framework

			ginkgo.BeforeEach(func(ctx context.Context) {
				f = setupRootfulPodman(ctx, initialDir)
			})

			ginkgo.Context("lifecycle commands", func() { //nolint:dupl
				ginkgo.It(
					"should run postAttachCommand on every attach",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-post-attach-every-time",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						gomega.Eventually(func() int {
							count, err := lifecycleMarkerCount(tempDir, ".devsy-post-attach.log")
							if err != nil {
								ginkgo.GinkgoWriter.Printf(
									"failed reading post-attach marker: %v\n",
									err,
								)
								return -1
							}
							return count
						}).WithTimeout(30 * time.Second).WithPolling(250 * time.Millisecond).Should(
							gomega.Equal(1),
						)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						gomega.Eventually(func() int {
							count, err := lifecycleMarkerCount(tempDir, ".devsy-post-attach.log")
							if err != nil {
								ginkgo.GinkgoWriter.Printf(
									"failed reading post-attach marker: %v\n",
									err,
								)
								return -1
							}
							return count
						}).WithTimeout(30 * time.Second).WithPolling(250 * time.Millisecond).Should(
							gomega.Equal(2),
						)
					},
					ginkgo.SpecTimeout(
						framework.TimeoutModerate(),
					),
				)

				ginkgo.It(
					"should run initializeCommand with object syntax",
					func(ctx context.Context) {
						tempDir, err := setupWorkspaceAndUp(
							ctx,
							"tests/up/testdata/docker-initcmd-parallel",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						one, err := os.ReadFile( //nolint:gosec // G304
							filepath.Join(tempDir, "init-cmd-one.out"),
						)
						framework.ExpectNoError(err)
						gomega.Expect(string(one)).To(gomega.Equal("initCmdOne"))

						two, err := os.ReadFile( //nolint:gosec // G304
							filepath.Join(tempDir, "init-cmd-two.out"),
						)
						framework.ExpectNoError(err)
						gomega.Expect(string(two)).To(gomega.Equal("initCmdTwo"))
					},
					ginkgo.SpecTimeout(framework.TimeoutModerate()),
				)

				ginkgo.It( //nolint:dupl // mirrors rootless lifecycle secrets-file test
					"should inject secrets-file env into lifecycle commands",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-secrets-file",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						secretsDir, err := framework.CreateTempDir()
						framework.ExpectNoError(err)
						ginkgo.DeferCleanup(func() { _ = os.RemoveAll(secretsDir) })

						secretsFile := filepath.Join(secretsDir, "secrets.json")
						err = os.WriteFile(
							secretsFile,
							[]byte(
								`{"MY_SECRET":"test-value-12345","ANOTHER_SECRET":"second-secret-42"}`,
							),
							0o600,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir, "--secrets-file", secretsFile)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat /tmp/secret-check.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal("test-value-12345"))

						out, err = f.DevsySSH(
							ctx, tempDir, "cat /tmp/another-secret-check.out",
						)
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal("second-secret-42"))
					},
					ginkgo.SpecTimeout(framework.TimeoutModerate()),
				)
			})
		})
	},
)
