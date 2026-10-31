package up

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// imageConfigArchOption sets ARCHITECTURE, which IMAGE_CONFIG_FILE requires. The
// e2e cluster runs on the host, so its nodes share the host's architecture.
const imageConfigArchOption = "ARCHITECTURE=" + runtime.GOARCH

const (
	imageConfigFileMarkerEnv   = "IMAGE_CONFIG_FILE_MARKER"
	imageConfigFileMarkerValue = "from-image-config-file"
)

// writeImageConfigFile writes an OCI-style image config whose
// devcontainer.metadata label sets a marker env var the real image lacks, so
// seeing the marker in the workspace proves the file was used instead of the
// registry.
func writeImageConfigFile() string {
	metadata, err := json.Marshal([]map[string]any{
		{"containerEnv": map[string]string{imageConfigFileMarkerEnv: imageConfigFileMarkerValue}},
	})
	framework.ExpectNoError(err)

	content, err := json.Marshal(map[string]any{
		"config": map[string]any{
			"Env":    []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			"Labels": map[string]string{"devcontainer.metadata": string(metadata)},
		},
	})
	framework.ExpectNoError(err)

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "image-config.json")
	framework.ExpectNoError(os.WriteFile(path, content, 0o600))
	return path
}

var _ = ginkgo.Describe(
	"kubernetes IMAGE_CONFIG_FILE option",
	ginkgo.Label("up-provider-kubernetes", "up-provider-kubernetes-image-config-file"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"applies the image config from the IMAGE_CONFIG_FILE file",
			func(ctx context.Context) {
				e := newIdleEnv(ctx, initialDir,
					"IMAGE_CONFIG_FILE="+writeImageConfigFile(), imageConfigArchOption)

				framework.ExpectNoError(e.up(ctx))
				gomega.Expect(e.ssh(ctx, "echo $"+imageConfigFileMarkerEnv)).
					To(gomega.Equal(imageConfigFileMarkerValue))
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)

		ginkgo.It(
			"fails clearly when the IMAGE_CONFIG_FILE file is missing",
			func(ctx context.Context) {
				missing := filepath.Join(ginkgo.GinkgoT().TempDir(), "missing.json")
				e := newIdleEnv(ctx, initialDir,
					"IMAGE_CONFIG_FILE="+missing, imageConfigArchOption)

				_, stderr, err := e.f.ExecCommandCapture(ctx,
					[]string{cmdWorkspace, "up", e.dir, flagIDEOption, kubernetesIgnoreIDE})
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(stderr).To(gomega.ContainSubstring("read image config file"))
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)
	},
)
