package up

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/devsy-org/devsy/e2e/framework"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const testDockerCommand = "docker"

var _ = ginkgo.Describe(
	"testing --update-remote-user-uid-default flag",
	ginkgo.Label("up-update-remote-user-uid"),
	func() {
		var dtc *dockerTestContext

		ginkgo.BeforeEach(func(ctx context.Context) {
			if runtime.GOOS != "linux" {
				ginkgo.Skip("updateRemoteUserUID only applies on Linux")
			}

			var err error
			dtc = &dockerTestContext{}
			dtc.initialDir, err = os.Getwd()
			framework.ExpectNoError(err)

			dtc.dockerHelper = &docker.DockerHelper{DockerCommand: testDockerCommand}
			dtc.f, err = setupDockerProvider(
				filepath.Join(dtc.initialDir, "bin"),
				testDockerCommand,
			)
			framework.ExpectNoError(err)
		})

		ginkgo.It("should accept --update-remote-user-uid-default=on", func(ctx context.Context) {
			_, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker",
				"--update-remote-user-uid-default", "on")
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("should accept --update-remote-user-uid-default=off", func(ctx context.Context) {
			tempDir, err := dtc.setupAndUp(ctx, "tests/up/testdata/docker",
				"--update-remote-user-uid-default", "off")
			framework.ExpectNoError(err)

			out, err := dtc.execSSH(ctx, tempDir, "id -u")
			framework.ExpectNoError(err)
			gomega.Expect(out).NotTo(gomega.BeEmpty())
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
	},
)
