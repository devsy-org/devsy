package features

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("features test", ginkgo.Label("features", "features-test"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)

		if _, lookErr := exec.LookPath("docker"); lookErr != nil {
			ginkgo.Skip("docker not available")
		}
	})

	ginkgo.It(
		"runs test scripts for discovered features",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			projectDir, err := os.MkdirTemp("", "e2e-features-test-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(projectDir) })

			srcDir := filepath.Join(projectDir, "src", "my-feature")
			framework.ExpectNoError(os.MkdirAll(srcDir, 0o750))
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(srcDir, "devcontainer-feature.json"),
				[]byte(`{"id":"my-feature","version":"1.0.0","name":"My Feature"}`),
				0o600,
			))
			// #nosec G306 -- test scripts must be executable
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(srcDir, "install.sh"),
				[]byte("#!/bin/bash\necho 'feature installed'\n"),
				0o750,
			))

			testDir := filepath.Join(projectDir, "test", "my-feature")
			framework.ExpectNoError(os.MkdirAll(testDir, 0o750))
			// #nosec G306 -- test scripts must be executable
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(testDir, "test.sh"),
				[]byte("#!/bin/bash\necho 'test passed'\nexit 0\n"),
				0o750,
			))

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "test",
				"--project-folder", projectDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("Feature Test Results"))
			gomega.Expect(stdout).To(gomega.ContainSubstring("PASS"))
			gomega.Expect(stdout).To(gomega.ContainSubstring("my-feature"))
		},
		ginkgo.SpecTimeout(framework.TimeoutModerate()),
	)

	ginkgo.It(
		"filters features with --features flag",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			projectDir, err := os.MkdirTemp("", "e2e-features-test-filter-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(projectDir) })

			for _, feat := range []string{"feat-a", "feat-b"} {
				srcDir := filepath.Join(projectDir, "src", feat)
				framework.ExpectNoError(os.MkdirAll(srcDir, 0o750))
				framework.ExpectNoError(os.WriteFile(
					filepath.Join(srcDir, "devcontainer-feature.json"),
					[]byte(`{"id":"`+feat+`","version":"1.0.0","name":"`+feat+`"}`),
					0o600,
				))
				// #nosec G306 -- test scripts must be executable
				framework.ExpectNoError(os.WriteFile(
					filepath.Join(srcDir, "install.sh"),
					[]byte("#!/bin/bash\necho installed\n"),
					0o750,
				))

				testDir := filepath.Join(projectDir, "test", feat)
				framework.ExpectNoError(os.MkdirAll(testDir, 0o750))
				// #nosec G306 -- test scripts must be executable
				framework.ExpectNoError(os.WriteFile(
					filepath.Join(testDir, "test.sh"),
					[]byte("#!/bin/bash\nexit 0\n"),
					0o750,
				))
			}

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "test",
				"--project-folder", projectDir,
				"--features", "feat-a",
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("feat-a"))
			gomega.Expect(stdout).NotTo(gomega.ContainSubstring("feat-b"))
		},
		ginkgo.SpecTimeout(framework.TimeoutModerate()),
	)

	ginkgo.It(
		"reports failure when test script exits non-zero",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			projectDir, err := os.MkdirTemp("", "e2e-features-test-fail-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(projectDir) })

			srcDir := filepath.Join(projectDir, "src", "bad-feature")
			framework.ExpectNoError(os.MkdirAll(srcDir, 0o750))
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(srcDir, "devcontainer-feature.json"),
				[]byte(`{"id":"bad-feature","version":"1.0.0","name":"Bad Feature"}`),
				0o600,
			))
			// #nosec G306 -- test scripts must be executable
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(srcDir, "install.sh"),
				[]byte("#!/bin/bash\necho installed\n"),
				0o750,
			))

			testDir := filepath.Join(projectDir, "test", "bad-feature")
			framework.ExpectNoError(os.MkdirAll(testDir, 0o750))
			// #nosec G306 -- test scripts must be executable
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(testDir, "test.sh"),
				[]byte("#!/bin/bash\necho 'test failed'\nexit 1\n"),
				0o750,
			))

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				"features", "test",
				"--project-folder", projectDir,
			})
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(stdout).To(gomega.ContainSubstring("FAIL"))
			gomega.Expect(stdout).To(gomega.ContainSubstring("bad-feature"))
		},
		ginkgo.SpecTimeout(framework.TimeoutModerate()),
	)
})
