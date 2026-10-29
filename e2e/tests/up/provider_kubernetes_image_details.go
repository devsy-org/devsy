package up

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	imageDetailsMarkerEnv   = "IMAGE_DETAILS_MARKER"
	imageDetailsMarkerValue = "from-image-details-file"
)

// writeImageDetailsFile writes an OCI-style image config whose
// devcontainer.metadata label sets a marker env var the real image lacks, so
// seeing the marker in the workspace proves the file was used instead of the
// registry.
func writeImageDetailsFile() string {
	metadata, err := json.Marshal([]map[string]any{
		{"containerEnv": map[string]string{imageDetailsMarkerEnv: imageDetailsMarkerValue}},
	})
	framework.ExpectNoError(err)

	content, err := json.Marshal(map[string]any{
		"config": map[string]any{
			"Env":    []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			"Labels": map[string]string{"devcontainer.metadata": string(metadata)},
		},
	})
	framework.ExpectNoError(err)

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "image-details.json")
	framework.ExpectNoError(os.WriteFile(path, content, 0o600))
	return path
}

var _ = ginkgo.Describe(
	"kubernetes IMAGE_DETAILS option",
	ginkgo.Label("up-provider-kubernetes", "up-provider-kubernetes-image-details"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"applies the image config from the IMAGE_DETAILS file",
			func(ctx context.Context) {
				e := newIdleEnv(ctx, initialDir, "IMAGE_DETAILS="+writeImageDetailsFile())

				framework.ExpectNoError(e.up(ctx))
				gomega.Expect(e.ssh(ctx, "echo $"+imageDetailsMarkerEnv)).
					To(gomega.Equal(imageDetailsMarkerValue))
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)

		ginkgo.It(
			"fails clearly when the IMAGE_DETAILS file is missing",
			func(ctx context.Context) {
				missing := filepath.Join(ginkgo.GinkgoT().TempDir(), "missing.json")
				e := newIdleEnv(ctx, initialDir, "IMAGE_DETAILS="+missing)

				_, stderr, err := e.f.ExecCommandCapture(ctx,
					[]string{cmdWorkspace, "up", e.dir, flagIDEOption, kubernetesIgnoreIDE})
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(stderr).To(gomega.ContainSubstring("read image details file"))
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)
	},
)
