// Package dockerbuildkit covers internal BuildKit against the provider-selected
// daemon on Windows, where the process environment points at a dead named pipe.
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
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	dockerdriver "github.com/devsy-org/devsy/pkg/driver/docker"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	dockerCmd = "docker"

	// Provisioned by the CI job: the SSH endpoint of the Docker Engine in WSL
	// and the daemon ID read from inside WSL.
	sshHostEnv   = "DEVSY_E2E_SSH_DOCKER_HOST"
	wslDaemonEnv = "DEVSY_E2E_WSL_DAEMON_ID"

	unreachableSSHHost = "ssh://devsy-ci-nobody@127.0.0.1:1"
)

// dockerOutput runs the docker CLI with env layered over the process
// environment, mirroring how the provider configures Devsy's own invocations.
func dockerOutput(ctx context.Context, env []string, args ...string) string {
	out, err := dockerRun(ctx, env, args...)
	framework.ExpectNoError(err, "docker %s: %s", strings.Join(args, " "), out)
	return out
}

func dockerRun(ctx context.Context, env []string, args ...string) (string, error) {
	//nolint:gosec // fixed binary, test-controlled args
	cmd := exec.CommandContext(ctx, dockerCmd, args...)
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return stderr.String(), err
	}
	return strings.TrimSpace(string(out)), nil
}

func requireEnv(name string) string {
	value := os.Getenv(name)
	gomega.Expect(value).NotTo(gomega.BeEmpty(), "%s must be provisioned by the CI job", name)
	return value
}

// providerEnv selects the daemon the way a provider's agent.docker.env does.
func providerEnv(host string) []string {
	return []string{"DOCKER_HOST=" + host, "DOCKER_CONTEXT="}
}

func newProductionDriver(host string) driver.ImageDriver {
	info := &provider.AgentWorkspaceInfo{Workspace: &provider.Workspace{}}
	info.Agent.Docker = provider.ProviderDockerDriverConfig{
		Path:    dockerCmd,
		Builder: "buildkit",
		Env:     map[string]string{"DOCKER_HOST": host, "DOCKER_CONTEXT": ""},
	}
	imageDriver, err := dockerdriver.NewDockerDriver(info)
	framework.ExpectNoError(err)
	return imageDriver
}

func newBuildRequest(tag string) driver.BuildRequest {
	dir := ginkgo.GinkgoT().TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	framework.ExpectNoError(os.WriteFile(
		dockerfile, []byte("FROM alpine\nRUN echo test > /marker\n"), 0o600,
	))
	return driver.BuildRequest{
		ParsedConfig: &config.SubstitutedConfig{
			Config: &config.DevContainerConfig{
				Origin: filepath.Join(dir, "devcontainer.json"),
			},
		},
		ExtendedBuildInfo: &feature.ExtendedBuildInfo{
			MetadataConfig: &config.ImageMetadataConfig{},
		},
		DockerfilePath:       dockerfile,
		LocalWorkspaceFolder: dir,
		PrebuildHash:         "latest",
		Options: provider.BuildOptions{
			// Unique image name so a stale image can never satisfy the assertions.
			CLIOptions: provider.CLIOptions{
				ForceBuild:            true,
				ForceInternalBuildKit: true,
				ImageName:             tag,
			},
		},
	}
}

func uniqueTag() string {
	return fmt.Sprintf("devsy-internal-buildkit:%d", time.Now().UnixNano())
}

// setProcessEnv overrides a process variable for one spec only.
func setProcessEnv(name, value string) {
	previous, had := os.LookupEnv(name)
	framework.ExpectNoError(os.Setenv(name, value))
	ginkgo.DeferCleanup(func() {
		if had {
			_ = os.Setenv(name, previous)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

var _ = ginkgo.Describe(
	"docker internal buildkit transport",
	ginkgo.Label("docker-internal-buildkit"),
	func() {
		var (
			sshHost string
			wslID   string
			envVars []string
		)

		ginkgo.BeforeEach(func(ctx context.Context) {
			if runtime.GOOS != "windows" {
				ginkgo.Skip("Windows only")
			}
			sshHost = requireEnv(sshHostEnv)
			wslID = requireEnv(wslDaemonEnv)
			envVars = providerEnv(sshHost)

			// The same assertion CI makes: the SSH endpoint reaches the WSL daemon,
			// not a local one.
			gomega.Expect(dockerOutput(ctx, envVars, "info", "--format", "{{.ID}}")).
				To(gomega.Equal(wslID), "provider SSH endpoint does not reach the WSL daemon")
		})

		ginkgo.It(
			"dials the provider's daemon through docker system dial-stdio",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				invocation, err := docker.NewDockerInvocation(&docker.DockerHelper{
					DockerCommand: dockerCmd,
					Environment:   envVars,
				})
				framework.ExpectNoError(err)
				host, ok := invocation.Lookup("DOCKER_HOST")
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(host).To(gomega.Equal(sshHost))

				conn, err := docker.NewStdioTransport(invocation).Dial(ctx)
				framework.ExpectNoError(err)
				framework.ExpectNoError(conn.Close())

				client, err := docker.NewClientWithInvocation(ctx, invocation)
				framework.ExpectNoError(err)
				defer func() { _ = client.Close() }()

				info, err := client.Info(ctx)
				framework.ExpectNoError(err, "docker system dial-stdio transport failed")
				gomega.Expect(info.ID).To(gomega.Equal(wslID))
			},
		)

		ginkgo.It(
			"loads an image built by raw BuildKit into the provider's daemon",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				contextDir := ginkgo.GinkgoT().TempDir()
				framework.ExpectNoError(os.WriteFile(
					filepath.Join(contextDir, "Dockerfile"),
					[]byte("FROM alpine\nRUN echo test > /marker\n"),
					0o600,
				))

				invocation, err := docker.NewDockerInvocation(&docker.DockerHelper{
					DockerCommand: dockerCmd,
					Environment:   envVars,
				})
				framework.ExpectNoError(err)
				client, err := docker.NewClientWithInvocation(ctx, invocation)
				framework.ExpectNoError(err)
				defer func() { _ = client.Close() }()

				buildKitClient, err := buildkit.NewDockerClient(ctx, client)
				framework.ExpectNoError(err)
				defer func() { _ = buildKitClient.Close() }()

				tag := uniqueTag()
				ginkgo.DeferCleanup(func(ctx context.Context) {
					_, _ = dockerRun(ctx, envVars, "rmi", "-f", tag)
				})

				var logs bytes.Buffer
				err = buildkit.Build(ctx, buildKitClient, &logs, "", &build.BuildOptions{
					Dockerfile: filepath.Join(contextDir, "Dockerfile"),
					Context:    contextDir,
					Images:     []string{tag},
					Load:       true,
				})
				framework.ExpectNoError(err, "internal BuildKit build failed:\n%s", logs.String())

				gomega.Expect(dockerOutput(ctx, envVars, "image", "inspect", "--format", "{{.Id}}", tag)).
					NotTo(gomega.BeEmpty(), "built image missing on the provider's daemon")
			},
		)

		ginkgo.It(
			"BuildDevContainer builds on the provider's SSH daemon despite a dead process DOCKER_HOST",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				// CI starts this binary with DOCKER_HOST pointing at a named pipe
				// that has no daemon, reproducing issue #1469's wrong fallback.
				processHost := os.Getenv("DOCKER_HOST")
				gomega.Expect(processHost).To(gomega.HavePrefix("npipe:"),
					"CI must start the e2e binary with a dead npipe DOCKER_HOST")
				_, err := dockerRun(ctx, nil, "info")
				gomega.Expect(err).To(gomega.HaveOccurred(),
					"process-env daemon must be unreachable for this regression to mean anything")

				tag := uniqueTag()
				ginkgo.DeferCleanup(func(ctx context.Context) {
					_, _ = dockerRun(ctx, envVars, "rmi", "-f", tag)
				})

				info, err := newProductionDriver(
					sshHost,
				).BuildDevContainer(ctx, newBuildRequest(tag))
				framework.ExpectNoError(err, "production BuildDevContainer failed")
				gomega.Expect(info.ImageName).To(gomega.Equal(tag))

				gomega.Expect(dockerOutput(ctx, envVars, "image", "inspect", "--format", "{{.Id}}", tag)).
					NotTo(gomega.BeEmpty(), "built image missing on the provider's daemon")
				gomega.Expect(dockerOutput(ctx, envVars, "info", "--format", "{{.ID}}")).
					To(gomega.Equal(wslID))
			},
		)

		ginkgo.It(
			"BuildDevContainer fails instead of falling back to the process daemon",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				// Make the process daemon reachable (the same WSL daemon) so a
				// silent fallback would succeed.
				setProcessEnv("DOCKER_HOST", sshHost)
				setProcessEnv("DOCKER_CONTEXT", "")

				tag := uniqueTag()
				ginkgo.DeferCleanup(func(ctx context.Context) {
					_, _ = dockerRun(ctx, nil, "rmi", "-f", tag)
				})

				_, err := newProductionDriver(unreachableSSHHost).
					BuildDevContainer(ctx, newBuildRequest(tag))
				gomega.Expect(err).To(gomega.HaveOccurred(),
					"build succeeded: provider DOCKER_HOST was ignored")

				_, err = dockerRun(ctx, nil, "image", "inspect", tag)
				gomega.Expect(err).To(gomega.HaveOccurred(),
					"image was built on the process daemon")
			},
		)
	},
)
