package up

import (
	"context"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for podman provider",
	ginkgo.Label("up-provider-podman-rootless-basic"),
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

				// Regression: rootless podman bind-mounts the workspace folder
				// root-owned, so a non-root remoteUser couldn't chdir into it
				// and the in-container SSH server failed with "fork/exec
				// /usr/bin/bash: permission denied".
				ginkgo.It(
					"should ssh into a workspace with a non-root remoteUser",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-nonroot-user",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						whoami, err := f.DevsySSH(ctx, tempDir, "whoami")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(whoami)).To(gomega.Equal("devsyuser"))

						owner, err := f.DevsySSH(ctx, tempDir, `stat -c %U "$PWD"`)
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(owner)).To(gomega.Equal("devsyuser"),
							"workspace folder should be chowned to the remote user")
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
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
