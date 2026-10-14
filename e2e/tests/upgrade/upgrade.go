package upgrade

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	cmdFeature          = "feature"
	cmdUpgrade          = "upgrade"
	flagWorkspaceFolder = "--workspace-folder"
	flagDryRun          = "--dry-run"
)

var _ = ginkgo.Describe("upgrade command", ginkgo.Label("upgrade"), func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It(
		"upgrades outdated feature version in devcontainer.json",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureRepo := regHost + "/test/my-feature"

			framework.PushFeatureTag(featureRepo + ":1.0.0")
			framework.PushFeatureTag(featureRepo + ":1.1.0")
			framework.PushFeatureTag(featureRepo + ":2.0.0")

			tempDir := writeDevcontainerJSON(fmt.Sprintf(`{
	"image": "ubuntu:22.04",
	"features": {"%s:1.0.0": {}}
}`, featureRepo))
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			_, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeature, cmdUpgrade,
				flagWorkspaceFolder, tempDir,
			})
			framework.ExpectNoError(err)

			updated := readDevcontainerJSON(tempDir)
			gomega.Expect(updated).To(gomega.ContainSubstring(featureRepo + ":2.0.0"))
			gomega.Expect(updated).NotTo(gomega.ContainSubstring(featureRepo + ":1.0.0"))
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"dry-run previews changes without modifying the file",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureRepo := regHost + "/test/my-feature"

			framework.PushFeatureTag(featureRepo + ":1.0.0")
			framework.PushFeatureTag(featureRepo + ":2.0.0")

			originalContent := fmt.Sprintf(`{
	"image": "ubuntu:22.04",
	"features": {"%s:1.0.0": {}}
}`, featureRepo)
			tempDir := writeDevcontainerJSON(originalContent)
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeature, cmdUpgrade,
				flagDryRun,
				flagWorkspaceFolder, tempDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(gomega.ContainSubstring("2.0.0"))

			afterContent := readDevcontainerJSON(tempDir)
			gomega.Expect(afterContent).To(
				gomega.ContainSubstring(featureRepo + ":1.0.0"),
			)
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"upgrades only targeted features when arguments are provided",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureA := regHost + "/test/feature-a"
			featureB := regHost + "/test/feature-b"

			framework.PushFeatureTag(featureA + ":1.0.0")
			framework.PushFeatureTag(featureA + ":2.0.0")
			framework.PushFeatureTag(featureB + ":1.0.0")
			framework.PushFeatureTag(featureB + ":2.0.0")

			tempDir := writeDevcontainerJSON(fmt.Sprintf(`{
	"image": "ubuntu:22.04",
	"features": {
		"%s:1.0.0": {},
		"%s:1.0.0": {}
	}
}`, featureA, featureB))
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			_, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeature, cmdUpgrade,
				flagWorkspaceFolder, tempDir,
				featureA,
			})
			framework.ExpectNoError(err)

			updated := readDevcontainerJSON(tempDir)
			gomega.Expect(updated).To(
				gomega.ContainSubstring(featureA + ":2.0.0"),
			)
			gomega.Expect(updated).To(
				gomega.ContainSubstring(featureB + ":1.0.0"),
			)
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"reports all up to date when no upgrades are available",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureRepo := regHost + "/test/my-feature"

			framework.PushFeatureTag(featureRepo + ":1.0.0")

			tempDir := writeDevcontainerJSON(fmt.Sprintf(`{
	"image": "ubuntu:22.04",
	"features": {"%s:1.0.0": {}}
}`, featureRepo))
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			stdout, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeature, cmdUpgrade,
				flagWorkspaceFolder, tempDir,
			})
			framework.ExpectNoError(err)

			gomega.Expect(stdout).To(
				gomega.ContainSubstring("All features are up to date."),
			)
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)

	ginkgo.It(
		"preserves JSONC comments when upgrading",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")

			srv := httptest.NewServer(registry.New())
			ginkgo.DeferCleanup(func() { srv.Close() })

			regHost := strings.TrimPrefix(srv.URL, "http://")
			featureRepo := regHost + "/test/my-feature"

			framework.PushFeatureTag(featureRepo + ":1.0.0")
			framework.PushFeatureTag(featureRepo + ":2.0.0")

			tempDir := writeDevcontainerJSON(fmt.Sprintf(`{
	// Important comment
	"image": "ubuntu:22.04",
	"features": {"%s:1.0.0": {}}
}`, featureRepo))
			ginkgo.DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

			_, _, err := f.ExecCommandCapture(ctx, []string{
				cmdFeature, cmdUpgrade,
				flagWorkspaceFolder, tempDir,
			})
			framework.ExpectNoError(err)

			updated := readDevcontainerJSON(tempDir)
			gomega.Expect(updated).To(gomega.ContainSubstring("// Important comment"))
			gomega.Expect(updated).To(
				gomega.ContainSubstring(featureRepo + ":2.0.0"),
			)
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)
})

func writeDevcontainerJSON(content string) string {
	tempDir, err := os.MkdirTemp("", "upgrade-test-*")
	framework.ExpectNoError(err)

	devcontainerDir := filepath.Join(tempDir, ".devcontainer")
	framework.ExpectNoError(os.MkdirAll(devcontainerDir, 0o750))
	framework.ExpectNoError(
		os.WriteFile(
			filepath.Join(devcontainerDir, "devcontainer.json"),
			[]byte(content),
			0o600,
		),
	)

	return tempDir
}

func readDevcontainerJSON(tempDir string) string {
	// #nosec G304 -- path constructed from test temp directory
	data, err := os.ReadFile(
		filepath.Join(tempDir, ".devcontainer", "devcontainer.json"),
	)
	framework.ExpectNoError(err)

	return string(data)
}
