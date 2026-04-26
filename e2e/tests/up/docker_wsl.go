package up

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("testing up command for windows", ginkgo.Label("up-docker-wsl"), func() {
	var f *framework.Framework
	var dockerHelper *docker.DockerHelper
	var initialDir string
	var err error

	ginkgo.BeforeEach(func(ctx context.Context) {
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
		dockerHelper = &docker.DockerHelper{DockerCommand: "podman"}
		f, err = setupDockerProvider(filepath.Join(initialDir, "bin"), "podman")
		framework.ExpectNoError(err)
	})

	ginkgo.It("should start a new workspace with existing image", func(ctx context.Context) {
		tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
		framework.ExpectNoError(err)

		err = f.DevsyUp(ctx, tempDir)
		framework.ExpectNoError(err)
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("should start a new workspace with mounts", func(ctx context.Context) {
		tempDir, err := setupWorkspace("tests/up/testdata/docker-mounts", initialDir, f)
		framework.ExpectNoError(err)

		err = f.DevsyUp(ctx, tempDir, "--debug")
		framework.ExpectNoError(err)

		workspace, err := f.FindWorkspace(ctx, tempDir)
		framework.ExpectNoError(err)
		projectName := workspace.ID

		ids, err := dockerHelper.FindContainer(ctx, []string{
			fmt.Sprintf("%s=%s", config.DockerIDLabel, workspace.UID),
		})
		framework.ExpectNoError(err)
		gomega.Expect(ids).To(gomega.HaveLen(1), "1 compose container to be created")

		foo, err := f.DevsySSH(ctx, projectName, "cat $HOME/mnt1/foo.txt")
		framework.ExpectNoError(err)
		gomega.Expect(strings.TrimSpace(foo)).To(gomega.Equal("BAR"))

		bar, err := f.DevsySSH(ctx, projectName, "cat $HOME/mnt2/bar.txt")
		framework.ExpectNoError(err)
		gomega.Expect(strings.TrimSpace(bar)).To(gomega.Equal("FOO"))
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It(
		"should start a new workspace with dotfiles - no install script",
		func(ctx context.Context) {
			tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
			framework.ExpectNoError(err)

			// Wait for devsy workspace to come online (deadline: 30s)
			err = f.DevsyUp(
				ctx,
				tempDir,
				"--dotfiles",
				"https://github.com/loft-sh/example-dotfiles",
			)
			framework.ExpectNoError(err)

			out, err := f.DevsySSH(ctx, tempDir, "ls ~/.file*")
			framework.ExpectNoError(err)

			expectedOutput := `/home/vscode/.file1
/home/vscode/.file2
/home/vscode/.file3
`
			framework.ExpectEqual(out, expectedOutput, "should match")
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"should start a new workspace with dotfiles - install script",
		func(ctx context.Context) {
			tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
			framework.ExpectNoError(err)

			err = f.DevsyUp(
				ctx,
				tempDir,
				"--dotfiles",
				"https://github.com/loft-sh/example-dotfiles",
				"--dotfiles-script",
				"install-example",
			)
			framework.ExpectNoError(err)

			out, err := f.DevsySSH(ctx, tempDir, "ls /tmp/worked")
			framework.ExpectNoError(err)

			expectedOutput := "/tmp/worked\n"

			framework.ExpectEqual(out, expectedOutput, "should match")
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"should start a new workspace with dotfiles - no install script, commit",
		func(ctx context.Context) {
			tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
			framework.ExpectNoError(err)

			err = f.DevsyUp(
				ctx,
				tempDir,
				"--dotfiles",
				"https://github.com/loft-sh/example-dotfiles@sha256:9a0b41808bf8f50e9871b3b5c9280fe22bf46a04",
			)
			framework.ExpectNoError(err)

			out, err := f.DevsySSH(ctx, tempDir, "ls ~/.file*")
			framework.ExpectNoError(err)

			expectedOutput := `/home/vscode/.file1
/home/vscode/.file2
/home/vscode/.file3
`
			framework.ExpectEqual(out, expectedOutput, "should match")
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"should start a new workspace with dotfiles - no install script, branch",
		func(ctx context.Context) {
			tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
			framework.ExpectNoError(err)

			err = f.DevsyUp(
				ctx,
				tempDir,
				"--dotfiles",
				"https://github.com/loft-sh/example-dotfiles@do-not-delete",
			)
			framework.ExpectNoError(err)

			out, err := f.DevsySSH(ctx, tempDir, "cat ~/.branch_test")
			framework.ExpectNoError(err)

			expectedOutput := "test\n"
			framework.ExpectEqual(out, expectedOutput, "should match")
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It("should start a new workspace with custom image", func(ctx context.Context) {
		tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
		framework.ExpectNoError(err)

		err = f.DevsyUp(ctx, tempDir, "--devcontainer-image", "alpine")
		framework.ExpectNoError(err)

		out, err := f.DevsySSH(ctx, tempDir, "grep ^ID= /etc/os-release")
		framework.ExpectNoError(err)

		expectedOutput := "ID=alpine\n"
		unexpectedOutput := "ID=debian\n"

		framework.ExpectEqual(out, expectedOutput, "should match")
		framework.ExpectNotEqual(out, unexpectedOutput, "should NOT match")
	}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It(
		"should start a new workspace with custom image and skip building",
		func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker-with-multi-stage-build",
				initialDir,
				f,
			)
			framework.ExpectNoError(err)

			err = f.DevsyUp(ctx, tempDir, "--devcontainer-image", "alpine")
			framework.ExpectNoError(err)

			out, err := f.DevsySSH(ctx, tempDir, "grep ^ID= /etc/os-release")
			framework.ExpectNoError(err)

			expectedOutput := "ID=alpine\n"
			unexpectedOutput := "ID=debian\n"

			framework.ExpectEqual(out, expectedOutput, "should match")
			framework.ExpectNotEqual(out, unexpectedOutput, "should NOT match")
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)
})
