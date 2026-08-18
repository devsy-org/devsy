package up

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for podman provider",
	ginkgo.Label("up-provider-podman-rootful-lifecycle"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.Context("with rootful podman", func() {
			var f *framework.Framework

			//nolint:dupl // shared rootful podman wrapper setup across split files
			ginkgo.BeforeEach(func(ctx context.Context) {
				wrapper, err := os.Create( //nolint:gosec // G304: test-controlled path
					initialDir + "/bin/podman-rootful",
				)
				framework.ExpectNoError(err)

				_, err = wrapper.WriteString("#!/bin/sh\nsudo podman \"$@\"\n")
				if err != nil {
					_ = wrapper.Close()
					framework.ExpectNoError(err)
				}

				err = wrapper.Close()
				framework.ExpectNoError(err)

				// #nosec G302 -- wrapper script needs execute permission
				err = os.Chmod(initialDir+"/bin/podman-rootful", 0o755)
				framework.ExpectNoError(err)

				cmd := exec.CommandContext( //nolint:gosec // G204: test-controlled path
					ctx, initialDir+"/bin/podman-rootful", "ps",
				)
				docker.PrepareForGroupCancellation(cmd)
				out, err := cmd.CombinedOutput()
				framework.ExpectNoError(err, string(out))

				ginkgo.DeferCleanup(func() {
					_ = os.Remove(initialDir + "/bin/podman-rootful")
				})

				f, err = setupDockerProvider(
					initialDir+"/bin",
					initialDir+"/bin/podman-rootful",
				)
				framework.ExpectNoError(err)
			},
			)

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

						out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/on-create.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("onCreateDone"))

						out, err = f.DevsySSH(ctx, tempDir, "cat $HOME/update-content.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("updateContentDone"))

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/deferred.marker 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postCreateDone"),
						)

						envPath, err := f.DevsySSH(
							ctx, tempDir, "cat $HOME/deferred-env-path.out",
						)
						framework.ExpectNoError(err)
						gomega.Expect(envPath).To(
							gomega.ContainSubstring("/usr/local/bin"),
						)
						gomega.Expect(envPath).NotTo(gomega.ContainSubstring("${containerEnv:"))

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx,
								tempDir,
								"cat $HOME/post-start-deferred.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
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
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/post-attach.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(15 * time.Second).WithPolling(500 * time.Millisecond).Should(
							gomega.Equal("postAttachDone"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})
		})
	},
)
