package dockerbuildkit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/wslg"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const wslgTestWindows = "windows"

var _ = ginkgo.Describe("WSLg environment transport", ginkgo.Label("wslg"), func() {
	ginkgo.DescribeTable("creates and cleans up a workspace using the selected WSL user's display",
		func(ctx context.Context, compose bool) {
			framework.SkipUnlessOS(wslgTestWindows, "native Windows WSL fixture required")
			target := wslg.Target{
				Distro: requireEnv("DEVSY_WSLG_TEST_DISTRO"),
				User:   requireEnv("DEVSY_WSLG_TEST_USER"),
			}
			snapshot, err := wslg.Discover(ctx, target)
			framework.ExpectNoError(err)
			display := snapshot.Environment()[wslg.Display]
			gomega.Expect(display).
				NotTo(gomega.BeEmpty(), "the X11 transport fixture needs DISPLAY")
			host := requireEnv(sshHostEnv)
			env := providerEnv(host)
			gomega.Expect(dockerOutput(ctx, env, "info", "--format", "{{.ID}}")).
				To(gomega.Equal(requireEnv(wslDaemonEnv)))

			// A private Devsy home keeps owner authorization and provider state isolated.
			framework.SetEnvForSpec(pkgconfig.EnvHome, ginkgo.GinkgoT().TempDir())
			framework.SetEnvForSpec(pkgconfig.EnvConfig, "")
			cwd, err := os.Getwd()
			framework.ExpectNoError(err)
			f := framework.NewDefaultFramework(filepath.Join(cwd, "bin"))
			framework.ExpectNoError(
				f.DevsyContextCreate(
					ctx,
					"wslg-test",
					"-o",
					"WSLG_ENABLED=true",
					"-o",
					"WSLG_DISTRO="+target.Distro,
					"-o",
					"WSLG_USER="+target.User,
					// Streaming the development agent over the runner's WSL SSH
					// connection can exceed the ordinary 20-second injection budget.
					"-o",
					"AGENT_INJECT_TIMEOUT=120",
				),
			)
			framework.ExpectNoError(f.DevsyContextUse(ctx, "wslg-test"))
			framework.ExpectNoError(
				f.DevsyProviderAdd(
					ctx,
					"docker",
					"-o",
					"DOCKER_HOST="+host,
					"-o",
					"DOCKER_CONTEXT=",
					"-o",
					"DOCKER_PATH=docker",
				),
			)
			framework.ExpectNoError(f.DevsyProviderUse(ctx, "docker"))

			dir := ginkgo.GinkgoT().TempDir()
			image := "ghcr.io/devsy-org/test-images/base:alpine"
			config := map[string]any{
				"image":        image,
				"containerEnv": map[string]string{"DISPLAY": "${localEnv:DEVSY_WSLG_DISPLAY}"},
				"runArgs": []string{
					"--mount",
					"type=bind,source=/tmp/.X11-unix,target=/tmp/.X11-unix,readonly",
				},
			}
			if compose {
				config = map[string]any{
					"dockerComposeFile": "compose.yaml",
					"service":           "dev",
					"workspaceFolder":   "/workspaces/test",
				}
				framework.ExpectNoError(
					os.WriteFile(
						filepath.Join(dir, "compose.yaml"),
						[]byte(
							"services:\n  dev:\n    image: "+image+`
    command: sleep infinity
    environment:
      DISPLAY: ${DEVSY_WSLG_DISPLAY:?required}
    volumes:
      - type: bind
        source: /tmp/.X11-unix
        target: /tmp/.X11-unix
        read_only: true
        bind:
          create_host_path: false
`,
						),
						0o600,
					),
				)
			}
			data, err := json.Marshal(config)
			framework.ExpectNoError(err)
			framework.ExpectNoError(
				os.WriteFile(filepath.Join(dir, ".devcontainer.json"), data, 0o600),
			)
			ginkgo.DeferCleanup(
				func(ctx context.Context) { framework.ExpectNoError(f.DevsyWorkspaceDelete(ctx, dir)) },
			)
			framework.ExpectNoError(f.DevsyUp(ctx, dir, "--ide", "none"))
			workspace, err := f.FindWorkspace(ctx, dir)
			framework.ExpectNoError(err)
			id := dockerOutput(
				ctx,
				env,
				"ps",
				"-q",
				"--filter",
				"label="+pkgconfig.DevcontainerIDLabel+"="+workspace.UID,
			)
			gomega.Expect(id).NotTo(gomega.BeEmpty())
			actual := dockerOutput(ctx, env, "inspect", "--format", "{{json .Config.Env}}", id)
			var containerEnv []string
			framework.ExpectNoError(json.Unmarshal([]byte(actual), &containerEnv))
			gomega.Expect(containerEnv).To(gomega.ContainElement("DISPLAY=" + display))
			gomega.Expect(containerEnv).
				NotTo(gomega.ContainElement(wslg.Display+"="+display), "discovery must not inject ordinary container environment")
			gomega.Expect(dockerOutput(ctx, env, "exec", id, "test", "-S", "/tmp/.X11-unix/X0")).
				To(gomega.BeEmpty())

			framework.ExpectNoError(f.DevsyStop(ctx, dir))
			_, stderr, err := f.ExecCommandCapture(
				ctx,
				[]string{"workspace", "up", dir, "--ide", "none"},
			)
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(stderr).To(gomega.ContainSubstring("--recreate"))
			framework.ExpectNoError(f.DevsyUp(ctx, dir, "--ide", "none", "--recreate"))
			// Invalidate discovery while the Docker daemon remains reachable. Stop and
			// delete must replay metadata rather than execute this unavailable target.
			_, _, err = f.ExecCommandCapture(
				ctx,
				[]string{"context", "set", "-o", "WSLG_DISTRO=devsy-ci-missing-distro"},
			)
			framework.ExpectNoError(err)
			framework.ExpectNoError(f.DevsyStop(ctx, dir))
			ginkgo.AddReportEntry(
				"WSLg acceptance scope",
				strings.Join(
					[]string{
						"Native WSL printenv, image/Compose interpolation and daemon-visible X11 socket tested.",
						"Stopped-container recreation and cleanup tested.",
						"Visible Windows windows and native Wayland rendering require manual acceptance.",
					},
					" ",
				),
			)
		},
		ginkgo.Entry("image devcontainer", false, ginkgo.SpecTimeout(framework.TimeoutLong())),
		ginkgo.Entry("Compose devcontainer", true, ginkgo.SpecTimeout(framework.TimeoutLong())),
	)
})
