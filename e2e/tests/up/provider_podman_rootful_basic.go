package up

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for podman provider",
	ginkgo.Label("up-provider-podman-rootful-basic"),
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

			ginkgo.Context("basic", func() {
				ginkgo.It(
					"should start a new workspace with existing image",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("build", func() {
				ginkgo.It(
					"should start a workspace with a multistage Dockerfile build",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-with-multi-stage-build",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)

				ginkgo.It(
					"should build and respect overrideCommand false",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-override-command-false",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("agent delivery", func() {
				ginkgo.It(
					"should deliver the agent binary and execute SSH commands",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						err = f.DevsySSHEchoTestString(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("exec", func() {
				ginkgo.It(
					"should execute commands inside the container via SSH",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "echo -n hello-podman")
						framework.ExpectNoError(err)
						framework.ExpectEqual(out, "hello-podman")

						out, err = f.DevsySSH(ctx, tempDir, "pwd")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).NotTo(gomega.BeEmpty())
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("cleanup", func() {
				ginkgo.It(
					"should delete workspace and clean up resources",
					func(ctx context.Context) {
						tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker")
						framework.ExpectNoError(err)
						ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						_, err = f.FindWorkspace(ctx, tempDir)
						framework.ExpectNoError(err)

						err = f.DevsyWorkspaceDelete(ctx, tempDir)
						framework.ExpectNoError(err)

						_, err = f.FindWorkspace(ctx, tempDir)
						framework.ExpectError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})
		})
	},
)
