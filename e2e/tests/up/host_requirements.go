package up

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command host requirements enforcement",
	ginkgo.Label("up-host-requirements"),
	func() {
		var dtc *dockerTestContext

		ginkgo.BeforeEach(func(ctx context.Context) {
			var err error
			dtc = &dockerTestContext{}
			dtc.initialDir, err = os.Getwd()
			framework.ExpectNoError(err)

			dtc.f, err = setupDockerProvider(
				filepath.Join(dtc.initialDir, "bin"), "docker",
			)
			framework.ExpectNoError(err)
		})

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
	},
)
