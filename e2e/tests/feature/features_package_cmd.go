package feature

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	cmdPackage           = "package"
	flagTarget           = "--target"
	flagOutputFolder     = "--output-folder"
	flagForceCleanOutput = "--force-clean-output-folder"
	flagOutput           = "--result-format"
	outputJSON           = "json"
	outputPlain          = "plain"
	featureNameGo        = "go"
	featureNameNode      = "node"
	featureVersion100    = "1.0.0"
)

var _ = ginkgo.Describe("feature package", ginkgo.Label("feature"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It(
		"packages feature directories into tgz archives",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + fileBinSuffix)

			targetDir, err := os.MkdirTemp("", "e2e-features-package-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(targetDir) })

			goDir := filepath.Join(targetDir, featureNameGo)
			framework.ExpectNoError(os.MkdirAll(goDir, 0o750))
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(goDir, fileDevcontainerJSON),
				[]byte(`{"id":"go","version":"1.2.0","name":"Go"}`),
				0o600,
			))
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(goDir, fileInstallSh),
				[]byte("#!/bin/bash\necho installing go\n"),
				0o600,
			))

			outputDir, err := os.MkdirTemp("", "e2e-features-package-output-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(outputDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeatures, cmdPackage,
				flagTarget, targetDir,
				flagOutputFolder, outputDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("go"))
			gomega.Expect(stdout).To(gomega.ContainSubstring("1.2.0"))

			archivePath := filepath.Join(outputDir, "devcontainer-feature-go.tgz")
			_, statErr := os.Stat(archivePath)
			gomega.Expect(statErr).NotTo(gomega.HaveOccurred())

			files := e2eReadTarGzEntries(archivePath)
			gomega.Expect(files).To(gomega.ContainElement("devcontainer-feature.json"))
			gomega.Expect(files).To(gomega.ContainElement("install.sh"))
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"packages multiple features",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + fileBinSuffix)

			targetDir, err := os.MkdirTemp("", "e2e-features-package-multi-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(targetDir) })

			for _, feat := range []struct{ id, version string }{
				{featureNameGo, featureVersion100},
				{featureNameNode, "2.0.0"},
			} {
				dir := filepath.Join(targetDir, feat.id)
				framework.ExpectNoError(os.MkdirAll(dir, 0o750))
				framework.ExpectNoError(os.WriteFile(
					filepath.Join(dir, fileDevcontainerJSON),
					[]byte(
						`{"id":"`+feat.id+`","version":"`+feat.version+`","name":"`+feat.id+`"}`,
					),
					0o600,
				))
				framework.ExpectNoError(os.WriteFile(
					filepath.Join(dir, fileInstallSh),
					[]byte("#!/bin/bash\n"),
					0o600,
				))
			}

			outputDir, err := os.MkdirTemp("", "e2e-features-package-multi-output-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(outputDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeatures, cmdPackage,
				flagTarget, targetDir,
				flagOutputFolder, outputDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring(featureNameGo))
			gomega.Expect(stdout).To(gomega.ContainSubstring(featureNameNode))

			_, statErr := os.Stat(filepath.Join(outputDir, "devcontainer-feature-go.tgz"))
			gomega.Expect(statErr).NotTo(gomega.HaveOccurred())
			_, statErr = os.Stat(filepath.Join(outputDir, "devcontainer-feature-node.tgz"))
			gomega.Expect(statErr).NotTo(gomega.HaveOccurred())
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"outputs JSON when --output=json is specified",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + fileBinSuffix)

			targetDir, err := os.MkdirTemp("", "e2e-features-package-json-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(targetDir) })

			goDir := filepath.Join(targetDir, featureNameGo)
			framework.ExpectNoError(os.MkdirAll(goDir, 0o750))
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(goDir, fileDevcontainerJSON),
				[]byte(`{"id":"go","version":"1.0.0","name":"Go"}`),
				0o600,
			))
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(goDir, fileInstallSh),
				[]byte("#!/bin/bash\n"),
				0o600,
			))

			outputDir, err := os.MkdirTemp("", "e2e-features-package-json-output-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(outputDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeatures, cmdPackage,
				flagTarget, targetDir,
				flagOutputFolder, outputDir,
				flagOutput, outputJSON,
			})
			framework.ExpectNoError(err)

			var results []map[string]any
			gomega.Expect(json.Unmarshal([]byte(stdout), &results)).To(gomega.Succeed())
			gomega.Expect(results).To(gomega.HaveLen(1))
			gomega.Expect(results[0]["featureId"]).To(gomega.Equal("go"))
			gomega.Expect(results[0]["version"]).To(gomega.Equal(featureVersion100))
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"cleans output folder with --force-clean-output-folder",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + fileBinSuffix)

			targetDir, err := os.MkdirTemp("", "e2e-features-package-clean-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(targetDir) })

			goDir := filepath.Join(targetDir, featureNameGo)
			framework.ExpectNoError(os.MkdirAll(goDir, 0o750))
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(goDir, fileDevcontainerJSON),
				[]byte(`{"id":"go","version":"1.0.0","name":"Go"}`),
				0o600,
			))
			framework.ExpectNoError(os.WriteFile(
				filepath.Join(goDir, fileInstallSh),
				[]byte("#!/bin/bash\n"),
				0o600,
			))

			outputDir, err := os.MkdirTemp("", "e2e-features-package-clean-output-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(outputDir) })

			oldFile := filepath.Join(outputDir, "stale-artifact.tgz")
			framework.ExpectNoError(os.WriteFile(oldFile, []byte("old"), 0o600))

			_, _, err = f.ExecCommandCapture(ctx, []string{
				cmdFeatures, cmdPackage,
				flagTarget, targetDir,
				flagOutputFolder, outputDir,
				flagForceCleanOutput,
			})
			framework.ExpectNoError(err)

			_, statErr := os.Stat(oldFile)
			gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())

			_, statErr = os.Stat(filepath.Join(outputDir, "devcontainer-feature-go.tgz"))
			gomega.Expect(statErr).NotTo(gomega.HaveOccurred())
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"fails with no features in target directory",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + fileBinSuffix)

			targetDir, err := os.MkdirTemp("", "e2e-features-package-empty-*")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(targetDir) })

			_, stderr, err := f.ExecCommandCapture(ctx, []string{
				cmdFeatures, cmdPackage,
				flagTarget, targetDir,
			})
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(stderr).To(gomega.ContainSubstring("no features found"))
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)
})

func e2eReadTarGzEntries(path string) []string {
	f, err := os.Open(path) // #nosec G304 -- test helper
	framework.ExpectNoError(err)
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	framework.ExpectNoError(err)
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	var files []string
	for {
		hdr, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		framework.ExpectNoError(readErr)
		files = append(files, hdr.Name)
	}
	return files
}
