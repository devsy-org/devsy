// Package dockerbuildkit covers internal BuildKit against the CLI's daemon on Windows.
package dockerbuildkit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/devcontainer/buildkit"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const dockerCmd = "docker"

func dockerOutput(ctx context.Context, env []string, args ...string) string {
	//nolint:gosec // fixed binary, test-controlled args
	cmd := exec.CommandContext(ctx, dockerCmd, args...)
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	framework.ExpectNoError(err, "docker %s: %s", strings.Join(args, " "), stderr.String())
	return strings.TrimSpace(string(out))
}

var _ = ginkgo.Describe(
	"docker internal buildkit transport",
	ginkgo.Label("docker-internal-buildkit"),
	func() {
		var helper *docker.DockerHelper

		ginkgo.BeforeEach(func(ctx context.Context) {
			if runtime.GOOS != "windows" {
				ginkgo.Skip("Windows only")
			}
			helper = &docker.DockerHelper{DockerCommand: dockerCmd}
			if err := helper.Ping(ctx); err != nil {
				ginkgo.Skip("docker daemon is unreachable: " + err.Error())
			}
			diag, _ := exec.CommandContext(ctx, dockerCmd, "version").CombinedOutput()
			ginkgo.GinkgoWriter.Printf("docker version:\n%s\n", diag)
		})

		ginkgo.It(
			"dials the daemon through docker system dial-stdio",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				invocation, err := docker.NewDockerInvocation(helper)
				framework.ExpectNoError(err)

				conn, err := docker.NewStdioTransport(invocation).Dial(ctx)
				framework.ExpectNoError(err)
				framework.ExpectNoError(conn.Close())

				client, err := docker.NewClientWithInvocation(ctx, invocation)
				framework.ExpectNoError(err)
				defer func() { _ = client.Close() }()

				_, err = client.Ping(ctx)
				framework.ExpectNoError(err, "docker system dial-stdio transport failed")
			},
		)

		ginkgo.It(
			"builds an image with internal BuildKit on the CLI's daemon",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				contextDir := ginkgo.GinkgoT().TempDir()
				framework.ExpectNoError(os.WriteFile(
					filepath.Join(contextDir, "Dockerfile"),
					[]byte("FROM alpine\nRUN echo test > /marker\n"),
					0o600,
				))

				invocation, err := docker.NewDockerInvocation(helper)
				framework.ExpectNoError(err)
				client, err := docker.NewClientWithInvocation(ctx, invocation)
				framework.ExpectNoError(err)
				defer func() { _ = client.Close() }()

				buildKitClient, err := buildkit.NewDockerClient(ctx, client)
				framework.ExpectNoError(err)
				defer func() { _ = buildKitClient.Close() }()

				tag := fmt.Sprintf("devsy-internal-buildkit:%d", time.Now().UnixNano())
				ginkgo.DeferCleanup(func(ctx context.Context) {
					//nolint:gosec // fixed binary, test-generated tag
					_ = exec.CommandContext(ctx, dockerCmd, "rmi", "-f", tag).Run()
				})

				var logs bytes.Buffer
				err = buildkit.Build(ctx, buildKitClient, &logs, "", &build.BuildOptions{
					Dockerfile: filepath.Join(contextDir, "Dockerfile"),
					Context:    contextDir,
					Images:     []string{tag},
					Load:       true,
				})
				framework.ExpectNoError(err, "internal BuildKit build failed:\n%s", logs.String())

				gomega.Expect(dockerOutput(ctx, nil, "image", "inspect", "--format", "{{.Id}}", tag)).
					NotTo(gomega.BeEmpty(), "built image missing on the CLI's daemon")
			},
		)

		ginkgo.It(
			"respects provider environment",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				endpoint := dockerOutput(
					ctx, nil, "context", "inspect", "--format", "{{.Endpoints.docker.Host}}",
				)
				cliDaemonID := dockerOutput(ctx, nil, "info", "--format", "{{.ID}}")

				providerEnv := "DOCKER_HOST=" + endpoint
				invocation, err := docker.NewDockerInvocation(&docker.DockerHelper{
					DockerCommand: dockerCmd,
					Environment:   []string{providerEnv},
				})
				framework.ExpectNoError(err)
				value, ok := invocation.Lookup("DOCKER_HOST")
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(value).To(gomega.Equal(endpoint))

				client, err := docker.NewClientWithInvocation(ctx, invocation)
				framework.ExpectNoError(err)
				defer func() { _ = client.Close() }()

				info, err := client.Info(ctx)
				framework.ExpectNoError(err)
				gomega.Expect(info.ID).To(gomega.Equal(cliDaemonID))

				unreachable, err := docker.NewDockerInvocation(&docker.DockerHelper{
					DockerCommand: dockerCmd,
					Environment:   []string{"DOCKER_HOST=tcp://127.0.0.1:1"},
				})
				framework.ExpectNoError(err)
				badClient, err := docker.NewClientWithInvocation(ctx, unreachable)
				framework.ExpectNoError(err)
				defer func() { _ = badClient.Close() }()
				_, err = badClient.Info(ctx)
				gomega.Expect(err).To(gomega.HaveOccurred(),
					"provider DOCKER_HOST was ignored: client still reached a daemon")
			},
		)
	},
)
