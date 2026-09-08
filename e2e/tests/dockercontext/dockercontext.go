package dockercontext

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	docker "github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"docker context test suite",
	ginkgo.Label("docker-context"),
	func() {
		var (
			initialDir     string
			activeEndpoint string
			dockerHelper   *docker.DockerHelper
			f              *framework.Framework
		)

		ginkgo.BeforeEach(func(ctx context.Context) {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)

			dockerHelper = &docker.DockerHelper{DockerCommand: "docker"}
			if pingErr := dockerHelper.Ping(ctx); pingErr != nil {
				ginkgo.Skip("docker daemon is unreachable: " + pingErr.Error())
			}

			diag := dockerHelper.RuntimeDiagnostics(ctx)
			activeEndpoint = diag["endpoint"]
			if activeEndpoint == "" || activeEndpoint == "<unknown>" {
				activeEndpoint = "unix:///var/run/docker.sock"
			}

			f, err = framework.SetupDockerProvider(filepath.Join(initialDir, "bin"), "docker")
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"persisted non-default Docker context survives credential injection",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				testContextName := fmt.Sprintf("devsy-test-%d", time.Now().UnixNano())

				//nolint:gosec // activeEndpoint is resolved from local docker info/diagnostics
				err := exec.CommandContext(
					ctx,
					"docker",
					"context",
					"create",
					testContextName,
					"--docker",
					"host="+activeEndpoint,
				).Run()
				framework.ExpectNoError(err)
				origContext, hasContext := os.LookupEnv("DOCKER_CONTEXT")
				origHost, hasHost := os.LookupEnv("DOCKER_HOST")
				_ = os.Unsetenv("DOCKER_CONTEXT")
				_ = os.Unsetenv("DOCKER_HOST")
				ginkgo.DeferCleanup(func() {
					if hasContext {
						_ = os.Setenv("DOCKER_CONTEXT", origContext)
					}
					if hasHost {
						_ = os.Setenv("DOCKER_HOST", origHost)
					}
				})

				origShow, showErr := exec.CommandContext(ctx, "docker", "context", "show").Output()
				framework.ExpectNoError(showErr)
				initialPersistedContext := strings.TrimSpace(string(origShow))
				if initialPersistedContext == "" {
					initialPersistedContext = "default"
				}
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = os.Unsetenv("DOCKER_CONTEXT")
					//nolint:gosec // initialPersistedContext was captured from docker context show
					_ = exec.CommandContext(cleanupCtx, "docker", "context", "use", initialPersistedContext).
						Run()
					//nolint:gosec // testContextName is unique to this test
					_ = exec.CommandContext(cleanupCtx, "docker", "context", "rm", testContextName).
						Run()
				})
				//nolint:gosec // testContextName is unique to this test
				err = exec.CommandContext(ctx, "docker", "context", "use", testContextName).Run()
				framework.ExpectNoError(err)

				tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
				ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

				stdout, stderr, err := f.DevsyUpStreams(ctx, tempDir)
				framework.ExpectNoError(err)

				combined := stdout + "\n" + stderr
				gomega.Expect(combined).To(gomega.ContainSubstring("context=" + testContextName))
			},
		)

		ginkgo.It(
			"explicit DOCKER_CONTEXT overrides the persisted default",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				explicitContextName := fmt.Sprintf("devsy-test-explicit-%d", time.Now().UnixNano())

				//nolint:gosec // activeEndpoint is resolved from local docker info/diagnostics
				err := exec.CommandContext(
					ctx,
					"docker",
					"context",
					"create",
					explicitContextName,
					"--docker",
					"host="+activeEndpoint,
				).Run()
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					//nolint:gosec // explicitContextName is unique to this test
					_ = exec.CommandContext(cleanupCtx, "docker", "context", "rm", explicitContextName).
						Run()
				})

				origContext, hasContext := os.LookupEnv("DOCKER_CONTEXT")
				_ = os.Setenv("DOCKER_CONTEXT", explicitContextName)
				ginkgo.DeferCleanup(func() {
					if hasContext {
						_ = os.Setenv("DOCKER_CONTEXT", origContext)
					} else {
						_ = os.Unsetenv("DOCKER_CONTEXT")
					}
				})

				tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
				ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

				stdout, stderr, err := f.DevsyUpStreams(ctx, tempDir)
				framework.ExpectNoError(err)

				combined := stdout + "\n" + stderr
				gomega.Expect(combined).
					To(gomega.ContainSubstring("context=" + explicitContextName))
				gomega.Expect(combined).
					To(gomega.ContainSubstring("docker_context=" + explicitContextName))
			},
		)
	},
)
