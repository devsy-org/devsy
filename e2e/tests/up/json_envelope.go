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
	"testing up command --result-format flag",
	ginkgo.Label("up-result-format"),
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

		ginkgo.It("emits JSON envelope with --result-format json", func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			stdout, _, err := dtc.f.DevsyUpStreams(ctx, tempDir, "--result-format", "json")
			framework.ExpectNoError(err)

			lines := strings.Split(strings.TrimSpace(stdout), "\n")
			gomega.Expect(lines).NotTo(gomega.BeEmpty())

			lastLine := lines[len(lines)-1]
			var envelope config.ResultEnvelope
			err = json.Unmarshal([]byte(lastLine), &envelope)
			framework.ExpectNoError(err)

			gomega.Expect(envelope.Outcome).To(gomega.Equal("success"))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It("suppresses JSON envelope with --result-format plain", func(ctx context.Context) {
			tempDir, err := setupWorkspace(
				"tests/up/testdata/docker",
				dtc.initialDir,
				dtc.f,
			)
			framework.ExpectNoError(err)

			stdout, _, err := dtc.f.DevsyUpStreams(ctx, tempDir, "--result-format", "plain")
			framework.ExpectNoError(err)

			for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
				var envelope config.ResultEnvelope
				if json.Unmarshal([]byte(line), &envelope) == nil {
					gomega.Expect(envelope.Outcome).To(gomega.BeEmpty(),
						"expected no JSON envelope in stdout, but found one: %s", line)
				}
			}
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

		ginkgo.It(
			"emits error envelope with --result-format json on failure",
			func(ctx context.Context) {
				tempDir, err := setupWorkspace(
					"tests/up/testdata/docker-invalid-bind-mount",
					dtc.initialDir,
					dtc.f,
				)
				framework.ExpectNoError(err)

				stdout, _, err := dtc.f.DevsyUpStreams(ctx, tempDir, "--result-format", "json")
				gomega.Expect(err).To(gomega.HaveOccurred())

				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				gomega.Expect(lines).NotTo(gomega.BeEmpty())

				lastLine := lines[len(lines)-1]
				var envelope config.ErrorEnvelope
				err = json.Unmarshal([]byte(lastLine), &envelope)
				framework.ExpectNoError(err)

				gomega.Expect(envelope.Outcome).To(gomega.Equal("error"))
				gomega.Expect(envelope.Message).NotTo(gomega.BeEmpty())
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)
	},
)
