package up

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/docker"
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

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/attach-count.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(15 * time.Second).WithPolling(1 * time.Second).Should(
							gomega.Equal("1"),
						)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/attach-count.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(15 * time.Second).WithPolling(1 * time.Second).Should(
							gomega.Equal("2"),
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
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})
		})
	},
)
