package up

import (
	"os"
	"os/exec"
	"path/filepath"

	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"GPU detection graceful fallback",
	ginkgo.Label("up-gpu-detection"),
	func() {
		ginkgo.It("should not error under the test environment's container runtime", func() {
			initialDir, err := os.Getwd()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			dockerCmd := "docker"
			if _, err := exec.LookPath("podman"); err == nil {
				if _, dockerErr := exec.LookPath("docker"); dockerErr != nil {
					dockerCmd = "podman"
				}
			}

			binDir := filepath.Join(initialDir, "bin")
			h := &docker.DockerHelper{DockerCommand: filepath.Join(binDir, dockerCmd)}
			if _, err := os.Stat(h.DockerCommand); os.IsNotExist(err) {
				h.DockerCommand = dockerCmd
			}

			available, err := h.GPUSupportEnabled()
			gomega.Expect(err).NotTo(gomega.HaveOccurred(),
				"GPU detection should not error regardless of runtime")
			ginkgo.GinkgoWriter.Printf("GPU available: %v (runtime: %s)\n", available, dockerCmd)
		})
	},
)
