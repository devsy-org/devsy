package up

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	sopsE2EPlaintext   = "SUPER_SECRET_TEST_VALUE_7B91"
	sopsE2EMounted     = "mounted-value-77"
	sopsE2EAgeIdentity = "AGE-SECRET-KEY-12UWYSAH2MRDQ5K4EWC4253PDTCSCS32Y5EFQ8TEN2SL3QYU2GN2SG88CZX" // gitleaks:allow
)

var _ = ginkgo.Describe(
	"SOPS secret sources",
	ginkgo.Label("up-provider-docker"),
	func() {
		var dtc *dockerTestContext

		ginkgo.BeforeEach(func(ctx context.Context) {
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

		ginkgo.It(
			"injects a registered SOPS source as env and mounted secrets",
			func(ctx context.Context) {
				useSOPSAgeIdentity()
				tempDir, err := setupWorkspace(
					"tests/up/testdata/docker-sops-source",
					dtc.initialDir,
					dtc.f,
				)
				framework.ExpectNoError(err)

				registerSOPSSource(ctx, dtc, "sops-e2e", filepath.Join(tempDir, "secrets.enc.yaml"))
				err = dtc.f.DevsyUp(
					ctx,
					tempDir,
					"--secret",
					"sops:sops-e2e/SOPS_E2E_SECRET,target=SOPS_ALIAS",
					"--secret",
					"sops:sops-e2e/TLS_KEY,type=mount,target=tls_key",
				)
				framework.ExpectNoError(err)

				envValue, err := dtc.execSSH(ctx, tempDir, "cat /tmp/sops-env-check.out")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(envValue)).To(gomega.Equal(sopsE2EPlaintext))

				mountValue, err := dtc.execSSH(ctx, tempDir, "cat /tmp/sops-mount-check.out")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(mountValue)).To(gomega.Equal(sopsE2EMounted))
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)

		ginkgo.It(
			"discovers repository-owned SOPS sources and attached secrets",
			func(ctx context.Context) {
				useSOPSAgeIdentity()
				tempDir, err := setupWorkspace(
					"tests/up/testdata/docker-sops-project",
					dtc.initialDir,
					dtc.f,
				)
				framework.ExpectNoError(err)

				err = dtc.f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)

				value, err := dtc.execSSH(ctx, tempDir, "cat /tmp/sops-project-check.out")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(value)).To(gomega.Equal(sopsE2EPlaintext))
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)

		ginkgo.It(
			"fails closed when SOPS credentials are unavailable without leaking plaintext",
			func(ctx context.Context) {
				useSOPSAgeIdentity()
				tempDir, err := setupWorkspace(
					"tests/up/testdata/docker-sops-source",
					dtc.initialDir,
					dtc.f,
				)
				framework.ExpectNoError(err)

				registerSOPSSource(
					ctx,
					dtc,
					"sops-failure",
					filepath.Join(tempDir, "secrets.enc.yaml"),
				)
				framework.ExpectNoError(os.Setenv("SOPS_AGE_KEY", ""))
				framework.ExpectNoError(
					os.Setenv("SOPS_AGE_KEY_FILE", filepath.Join(tempDir, "missing-age-key")),
				)

				stdout, stderr, upErr := dtc.f.DevsyUpStreams(
					ctx,
					tempDir,
					"--secret",
					"sops:sops-failure/SOPS_E2E_SECRET",
				)
				gomega.Expect(upErr).To(gomega.HaveOccurred())

				combined := strings.Join([]string{stdout, stderr, fmt.Sprint(upErr)}, "\n")
				gomega.Expect(combined).NotTo(gomega.ContainSubstring(sopsE2EPlaintext))
				gomega.Expect(combined).NotTo(gomega.ContainSubstring(sopsE2EMounted))
			},
			ginkgo.SpecTimeout(framework.TimeoutShort()),
		)
	},
)

func useSOPSAgeIdentity() {
	setSOPSEnv("SOPS_AGE_KEY", sopsE2EAgeIdentity)
	setSOPSEnv("SOPS_AGE_KEY_FILE", "")
	setSOPSEnv("SOPS_AGE_KEY_CMD", "")
}

func setSOPSEnv(name, value string) {
	previous, had := os.LookupEnv(name)
	framework.ExpectNoError(os.Setenv(name, value))
	ginkgo.DeferCleanup(func() {
		if had {
			_ = os.Setenv(name, previous)
			return
		}
		_ = os.Unsetenv(name)
	})
}

func registerSOPSSource(ctx context.Context, dtc *dockerTestContext, name, filePath string) {
	_, err := dtc.f.ExecCommandOutput(
		ctx,
		[]string{secretCmd, "source", "add", "sops", name, filePath},
	)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(func() {
		_, _ = dtc.f.ExecCommandOutput(ctx, []string{secretCmd, "source", "remove", name})
	})
}
