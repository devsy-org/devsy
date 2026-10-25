package feature

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	cmdFeatures           = "feature"
	cmdInfo               = "info"
	cmdTest               = "test"
	featureNameA          = "feat-a"
	flagProjectFolder     = "--project-folder"
	dirSrc                = "src"
	fileBinSuffix         = "/bin"
	fileDevcontainerJSON  = "devcontainer-feature.json"
	fileInstallSh         = "install.sh"
	fileTestSh            = "test.sh"
	featureNameMyFeature  = "my-feature"
	featureNameBadFeature = "bad-feature"
)

const installScriptEchoInstalled = "#!/bin/bash\necho installed\n"

var _ = ginkgo.Describe("feature test", ginkgo.Label("feature"), func() {
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
			f := framework.NewDefaultFramework(initialDir + fileBinSuffix)

			projectDir := newFeatureProjectDir("e2e-features-test-*")
			writeFeatureFixtures(projectDir, featureFixture{
				id:            featureNameMyFeature,
				displayName:   "My Feature",
				installScript: "#!/bin/bash\necho 'feature installed'\n",
				testScript:    "#!/bin/bash\necho 'test passed'\nexit 0\n",
			})

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeatures, cmdTest,
				flagProjectFolder, projectDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("Feature Test Results"))
			gomega.Expect(stdout).To(gomega.ContainSubstring("PASS"))
			gomega.Expect(stdout).To(gomega.ContainSubstring(featureNameMyFeature))
		},
		ginkgo.SpecTimeout(framework.TimeoutModerate()),
	)

	ginkgo.It(
		"filters features with --features flag",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + fileBinSuffix)

			projectDir := newFeatureProjectDir("e2e-features-test-filter-*")
			writeFeatureFixtures(projectDir,
				featureFixture{
					id:            featureNameA,
					displayName:   featureNameA,
					installScript: installScriptEchoInstalled,
					testScript:    "#!/bin/bash\nexit 0\n",
				},
				featureFixture{
					id:            "feat-b",
					displayName:   "feat-b",
					installScript: installScriptEchoInstalled,
					testScript:    "#!/bin/bash\nexit 0\n",
				},
			)

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeatures, cmdTest,
				flagProjectFolder, projectDir,
				names.Flag(names.Features), featureNameA,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring(featureNameA))
			gomega.Expect(stdout).NotTo(gomega.ContainSubstring("feat-b"))
		},
		ginkgo.SpecTimeout(framework.TimeoutModerate()),
	)

	ginkgo.It(
		"reports failure when test script exits non-zero",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + fileBinSuffix)

			projectDir := newFeatureProjectDir("e2e-features-test-fail-*")
			writeFeatureFixtures(projectDir, featureFixture{
				id:            featureNameBadFeature,
				displayName:   "Bad Feature",
				installScript: installScriptEchoInstalled,
				testScript:    "#!/bin/bash\necho 'test failed'\nexit 1\n",
			})

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeatures, cmdTest,
				flagProjectFolder, projectDir,
			})
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(stdout).To(gomega.ContainSubstring("FAIL"))
			gomega.Expect(stdout).To(gomega.ContainSubstring(featureNameBadFeature))
		},
		ginkgo.SpecTimeout(framework.TimeoutModerate()),
	)
})

func newFeatureProjectDir(pattern string) string {
	projectDir, err := os.MkdirTemp("", pattern)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(func() { _ = os.RemoveAll(projectDir) })
	return projectDir
}

type featureFixture struct {
	id            string
	displayName   string
	installScript string
	testScript    string
}

func writeFeatureFixtures(projectDir string, features ...featureFixture) {
	for _, feature := range features {
		srcDir := filepath.Join(projectDir, dirSrc, feature.id)
		framework.ExpectNoError(os.MkdirAll(srcDir, 0o750))
		framework.ExpectNoError(os.WriteFile(
			filepath.Join(srcDir, fileDevcontainerJSON),
			[]byte(`{"id":"`+feature.id+`","version":"1.0.0","name":"`+feature.displayName+`"}`),
			0o600,
		))
		// #nosec G306 -- test scripts must be executable
		framework.ExpectNoError(os.WriteFile(
			filepath.Join(srcDir, fileInstallSh),
			[]byte(feature.installScript),
			0o750,
		))

		testDir := filepath.Join(projectDir, cmdTest, feature.id)
		framework.ExpectNoError(os.MkdirAll(testDir, 0o750))
		// #nosec G306 -- test scripts must be executable
		framework.ExpectNoError(os.WriteFile(
			filepath.Join(testDir, fileTestSh),
			[]byte(feature.testScript),
			0o750,
		))
	}
}
