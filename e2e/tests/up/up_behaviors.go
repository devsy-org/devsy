package up

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const testDockerCommand = "docker"

var _ = ginkgo.Describe("up command behaviors", ginkgo.Label("up-behaviors"), func() {
	var dtc *dockerTestContext

	ginkgo.BeforeEach(func(ctx context.Context) {
		var err error
		dtc = &dockerTestContext{}
		dtc.initialDir, err = os.Getwd()
		framework.ExpectNoError(err)

		dtc.dockerHelper = &docker.DockerHelper{DockerCommand: testDockerCommand}
		dtc.f, err = setupDockerProvider(
			filepath.Join(dtc.initialDir, "bin"), testDockerCommand,
		)
		framework.ExpectNoError(err)
	})

	ginkgo.It(
		"containerEnv preserves container-scoped vars as literals",
		func(ctx context.Context) {
			tempDir, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker-varsub-scope")
			framework.ExpectNoError(err)

			workspace, err := dtc.f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)

			cwf, err := dtc.execSSHCapture(ctx, workspace.ID, "cat $HOME/container-env-cwf.out")
			framework.ExpectNoError(err)
			gomega.Expect(cwf).To(gomega.ContainSubstring("/workspaces/"),
				"containerEnv containerWorkspaceFolder should resolve at runtime via shell")

			cwfb, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/container-env-cwfb.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(cwfb).To(gomega.Equal(filepath.Base(tempDir)),
				"containerEnv containerWorkspaceFolderBasename should resolve at runtime via shell")

			cenv, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/container-env-cenv.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(cenv).To(gomega.ContainSubstring("/usr/local/bin"),
				"containerEnv containerEnv:PATH should resolve at runtime via SubstituteContainerEnv")

			local, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/container-env-local.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(framework.CleanString(local)).
				To(gomega.Equal(framework.CleanString(tempDir)),
					"containerEnv localWorkspaceFolder should resolve at substitution time")

			remoteCwf, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/remote-env-cwf.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(framework.CleanString(remoteCwf)).
				To(gomega.ContainSubstring("/workspaces/"),
					"remoteEnv containerWorkspaceFolder should resolve at substitution time")

			remoteCwfb, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/remote-env-cwfb.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(remoteCwfb).To(gomega.Equal(filepath.Base(tempDir)),
				"remoteEnv containerWorkspaceFolderBasename should resolve at substitution time")

			remoteCenv, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/remote-env-cenv.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(remoteCenv).To(gomega.ContainSubstring("/usr/local/bin"),
				"remoteEnv containerEnv:PATH should resolve via SubstituteContainerEnv")

			remoteLocal, err := dtc.execSSHCapture(
				ctx,
				workspace.ID,
				"cat $HOME/remote-env-local.out",
			)
			framework.ExpectNoError(err)
			gomega.Expect(framework.CleanString(remoteLocal)).
				To(gomega.Equal(framework.CleanString(tempDir)),
					"remoteEnv localWorkspaceFolder should resolve at substitution time")
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It("should accept --update-remote-user-uid-default=on", func(ctx context.Context) {
		if runtime.GOOS != "linux" {
			ginkgo.Skip("updateRemoteUserUID only applies on Linux")
		}
		_, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker",
			"--update-remote-user-uid-default", "on")
		framework.ExpectNoError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should accept --update-remote-user-uid-default=off", func(ctx context.Context) {
		if runtime.GOOS != "linux" {
			ginkgo.Skip("updateRemoteUserUID only applies on Linux")
		}
		tempDir, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker",
			"--update-remote-user-uid-default", "off")
		framework.ExpectNoError(err)

		out, err := dtc.execSSH(ctx, tempDir, "id -u")
		framework.ExpectNoError(err)
		gomega.Expect(out).NotTo(gomega.BeEmpty())
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("emits JSON envelope with --result-format json", func(ctx context.Context) {
		tempDir, err := setupWorkspace(
			"tests/up/testdata/docker",
			dtc.initialDir,
			dtc.f,
		)
		framework.ExpectNoError(err)

		stdout, _, err := dtc.f.DevsyUpStreams(ctx, tempDir, "--result-format", "json")
		framework.ExpectNoError(err)

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		gomega.Expect(lines).NotTo(gomega.BeEmpty())

		lastLine := lines[len(lines)-1]
		var envelope config.ResultEnvelope
		err = json.Unmarshal([]byte(lastLine), &envelope)
		framework.ExpectNoError(err)

		gomega.Expect(envelope.Outcome).To(gomega.Equal("success"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("suppresses JSON envelope with --result-format plain", func(ctx context.Context) {
		tempDir, err := setupWorkspace(
			"tests/up/testdata/docker",
			dtc.initialDir,
			dtc.f,
		)
		framework.ExpectNoError(err)

		stdout, _, err := dtc.f.DevsyUpStreams(ctx, tempDir, "--result-format", "plain")
		framework.ExpectNoError(err)

		for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
			var envelope config.ResultEnvelope
			if json.Unmarshal([]byte(line), &envelope) == nil {
				gomega.Expect(envelope.Outcome).To(gomega.BeEmpty(),
					"expected no JSON envelope in stdout, but found one: %s", line)
			}
		}
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It(
		"emits error envelope with --result-format json on failure",
		func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker-invalid-bind-mount",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			stdout, _, err := dtc.f.DevsyUpStreams(ctx, tempDir, "--result-format", "json")
			gomega.Expect(err).To(gomega.HaveOccurred())

			lines := strings.Split(strings.TrimSpace(stdout), "\n")
			gomega.Expect(lines).NotTo(gomega.BeEmpty())

			lastLine := lines[len(lines)-1]
			var envelope config.ErrorEnvelope
			err = json.Unmarshal([]byte(lastLine), &envelope)
			framework.ExpectNoError(err)

			gomega.Expect(envelope.Outcome).To(gomega.Equal("error"))
			gomega.Expect(envelope.Message).NotTo(gomega.BeEmpty())
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It("blocks when host requirements unmet", func(ctx context.Context) {
		tempDir, err := setupWorkspace(
			"tests/up/testdata/docker-host-requirements",
			dtc.initialDir,
			dtc.f,
		)
		framework.ExpectNoError(err)

		stdout, _, err := dtc.f.DevsyUpStreams(ctx, tempDir)
		gomega.Expect(err).To(gomega.HaveOccurred(),
			"devsy up should fail when host requirements not met")

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		gomega.Expect(lines).NotTo(gomega.BeEmpty())
		lastLine := lines[len(lines)-1]

		var envelope config.ErrorEnvelope
		err = json.Unmarshal([]byte(lastLine), &envelope)
		framework.ExpectNoError(err)
		gomega.Expect(envelope.Outcome).To(gomega.Equal("error"))
		gomega.Expect(envelope.Message).To(
			gomega.ContainSubstring("minimum requirements"),
		)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("skip-host-requirements bypasses enforcement", func(ctx context.Context) {
		tempDir, err := setupWorkspace(
			"tests/up/testdata/docker-host-requirements",
			dtc.initialDir,
			dtc.f,
		)
		framework.ExpectNoError(err)

		stdout, _, err := dtc.f.DevsyUpStreams(
			ctx, tempDir, "--skip-host-requirements",
		)
		framework.ExpectNoError(err)

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		gomega.Expect(lines).NotTo(gomega.BeEmpty())

		lastLine := lines[len(lines)-1]
		var envelope config.ResultEnvelope
		err = json.Unmarshal([]byte(lastLine), &envelope)
		framework.ExpectNoError(err)

		gomega.Expect(envelope.Outcome).To(gomega.Equal("success"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It(
		"GPU detection does not error under the test environment's container runtime",
		func() {
			dockerCmd := testDockerCommand
			if _, err := exec.LookPath("podman"); err == nil {
				if _, dockerErr := exec.LookPath(testDockerCommand); dockerErr != nil {
					dockerCmd = "podman"
				}
			}

			binDir := filepath.Join(dtc.initialDir, "bin")
			h := &docker.DockerHelper{DockerCommand: filepath.Join(binDir, dockerCmd)}
			if _, err := os.Stat(h.DockerCommand); os.IsNotExist(err) {
				h.DockerCommand = dockerCmd
			}

			available, err := h.GPUSupportEnabled()
			gomega.Expect(err).NotTo(gomega.HaveOccurred(),
				"GPU detection should not error regardless of runtime")
			ginkgo.GinkgoWriter.Printf("GPU available: %v (runtime: %s)\n", available, dockerCmd)
		},
	)

	ginkgo.It("dotfiles without install script", func(ctx context.Context) {
		tempDir, err := dtc.setupAndUp(
			ctx,
			"tests/up/testdata/docker",
			"--dotfiles",
			"https://github.com/loft-sh/example-dotfiles",
		)
		framework.ExpectNoError(err)

		out, err := dtc.execSSH(ctx, tempDir, "ls ~/.file*")
		framework.ExpectNoError(err)
		framework.ExpectEqual(
			out,
			"/home/vscode/.file1\n/home/vscode/.file2\n/home/vscode/.file3\n",
		)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("dotfiles with install script", func(ctx context.Context) {
		tempDir, err := dtc.setupAndUp(
			ctx,
			"tests/up/testdata/docker",
			"--dotfiles",
			"https://github.com/loft-sh/example-dotfiles",
			"--dotfiles-script",
			"install-example",
		)
		framework.ExpectNoError(err)

		out, err := dtc.execSSH(ctx, tempDir, "ls /tmp/worked")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, "/tmp/worked\n")
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("dotfiles at specific commit", func(ctx context.Context) {
		tempDir, err := dtc.setupAndUp(
			ctx,
			"tests/up/testdata/docker",
			"--dotfiles",
			"https://github.com/loft-sh/example-dotfiles@sha256:9a0b41808bf8f50e9871b3b5c9280fe22bf46a04",
		)
		framework.ExpectNoError(err)

		out, err := dtc.execSSH(ctx, tempDir, "ls ~/.file*")
		framework.ExpectNoError(err)
		framework.ExpectEqual(
			out,
			"/home/vscode/.file1\n/home/vscode/.file2\n/home/vscode/.file3\n",
		)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("dotfiles at specific branch", func(ctx context.Context) {
		tempDir, err := dtc.setupAndUp(
			ctx,
			"tests/up/testdata/docker",
			"--dotfiles",
			"https://github.com/loft-sh/example-dotfiles@do-not-delete",
		)
		framework.ExpectNoError(err)

		out, err := dtc.execSSH(ctx, tempDir, "cat ~/.branch_test")
		framework.ExpectNoError(err)
		framework.ExpectEqual(out, "test\n")
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("dotfiles installed between postCreate and postStart", func(ctx context.Context) {
		tempDir, err := dtc.setupAndUp(
			ctx,
			"tests/up/testdata/docker-dotfiles-lifecycle-order",
			"--dotfiles",
			"https://github.com/loft-sh/example-dotfiles",
			"--dotfiles-script",
			"install-example",
		)
		framework.ExpectNoError(err)

		_, err = dtc.execSSH(ctx, tempDir, "test -f /tmp/worked")
		framework.ExpectNoError(err)

		out, err := dtc.execSSH(ctx, tempDir, "cat /tmp/lifecycle-order.log")
		framework.ExpectNoError(err)

		lines := strings.Split(strings.TrimSpace(out), "\n")
		gomega.Expect(lines).To(gomega.Equal([]string{
			"postCreate",
			"dotfiles-before-postStart",
			"postStart",
		}), "lifecycle ordering should be: postCreate → dotfiles → postStart")
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
