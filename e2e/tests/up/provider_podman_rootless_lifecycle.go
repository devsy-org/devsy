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
	ginkgo.Label("up-provider-podman-rootless-lifecycle"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.Context("with rootless podman", func() {
			var f *framework.Framework

			ginkgo.BeforeEach(func(ctx context.Context) {
				var err error
				f, err = setupDockerProvider(initialDir+"/bin", "podman")
				framework.ExpectNoError(err)
			})

			ginkgo.Context("lifecycle commands", func() { //nolint:dupl
				ginkgo.It(
					"should run postCreateCommand with object syntax",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-postcreate-parallel",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						one, err := f.DevsySSH(ctx, tempDir, "cat /tmp/post-create-one.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(one)).To(gomega.Equal("postCreateOne"))

						two, err := f.DevsySSH(ctx, tempDir, "cat /tmp/post-create-two.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(two)).To(gomega.Equal("postCreateTwo"))
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It("should run postStartCommand after restart", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-post-start-restart",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/post-start-count.log")
					framework.ExpectNoError(err)
					lines := strings.Count(strings.TrimSpace(out), "\n") + 1
					gomega.Expect(lines).To(gomega.Equal(1),
						"postStartCommand should have run once after initial up")

					err = f.DevsyWorkspaceStop(ctx, tempDir)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir)
					framework.ExpectNoError(err)

					out, err = f.DevsySSH(ctx, tempDir, "cat $HOME/post-start-count.log")
					framework.ExpectNoError(err)
					lines = strings.Count(strings.TrimSpace(out), "\n") + 1
					gomega.Expect(lines).To(gomega.Equal(2),
						"postStartCommand should have run again after restart")
				}, ginkgo.SpecTimeout(framework.TimeoutModerate()))

				ginkgo.It(
					"should defer postCreateCommand to background with waitFor",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-waitfor",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := readLifecycleFile(tempDir, "on-create.out")
						framework.ExpectNoError(err)
						gomega.Expect(out).To(gomega.Equal("onCreateDone"))

						out, err = readLifecycleFile(tempDir, "update-content.out")
						framework.ExpectNoError(err)
						gomega.Expect(out).To(gomega.Equal("updateContentDone"))

						gomega.Eventually(func() (string, error) {
							return readLifecycleFile(tempDir, "deferred.marker")
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postCreateDone"),
						)

						envPath, err := readLifecycleFile(tempDir, "deferred-env-path.out")
						framework.ExpectNoError(err)
						gomega.Expect(envPath).To(
							gomega.ContainSubstring("/usr/local/bin"),
						)
						gomega.Expect(envPath).NotTo(gomega.ContainSubstring("${containerEnv:"))

						gomega.Eventually(func() (string, error) {
							return readLifecycleFile(tempDir, "post-start-deferred.out")
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postStartDone"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should make IDE accessible before postAttachCommand completes",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-post-attach-nonblocking",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/post-start.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("postStartDone"))

						_, err = f.DevsySSH(ctx, tempDir, "cat $HOME/post-attach.out")
						gomega.Expect(err).To(gomega.HaveOccurred(),
							"postAttachCommand must still be blocked on the release marker")

						_, err = f.DevsySSH(ctx, tempDir, "touch $HOME/release-post-attach")
						framework.ExpectNoError(err)

						gomega.Eventually(func() string {
							out, err := probeSSH(f,
								ctx, tempDir, "cat $HOME/post-attach.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(30 * time.Second).WithPolling(500 * time.Millisecond).Should(
							gomega.Equal("postAttachDone"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

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
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
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
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})
		})
	},
)
