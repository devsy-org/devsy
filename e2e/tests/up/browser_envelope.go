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
	"testing up command result envelope for browser IDEs",
	ginkgo.Label("up-browser-envelope"),
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

		ginkgo.It(
			"emits success envelope for openvscode with --result-format json",
			func(ctx context.Context) {
				tempDir, err := setupWorkspace(
					"tests/up/testdata/docker",
					dtc.initialDir,
					dtc.f,
				)
				framework.ExpectNoError(err)

				stdout, _, err := dtc.f.DevsyUpStreamsRaw(
					ctx,
					tempDir,
					"--ide=openvscode",
					"--open-ide=false",
					"--result-format", "json",
				)
				framework.ExpectNoError(err)

				// Parse stdout line-by-line; assert exactly one JSON object with
				// outcome == "success" and non-empty container/user/workspace fields.
				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				gomega.Expect(lines).NotTo(gomega.BeEmpty())

				var matched []config.ResultEnvelope
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					var env config.ResultEnvelope
					if err := json.Unmarshal([]byte(line), &env); err != nil {
						continue
					}
					if env.Outcome == "success" {
						matched = append(matched, env)
					}
				}

				gomega.Expect(matched).
					To(gomega.HaveLen(1),
						"expected exactly one success envelope in stdout, got %d", len(matched))

				env := matched[0]
				gomega.Expect(env.ContainerID).NotTo(gomega.BeEmpty())
				gomega.Expect(env.RemoteUser).NotTo(gomega.BeEmpty())
				gomega.Expect(env.RemoteWorkspaceFolder).NotTo(gomega.BeEmpty())
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)
	},
)
