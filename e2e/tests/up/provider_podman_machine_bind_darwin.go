package up

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	podmanMachineCommand = "machine"
	podmanJSONFormat     = "json"
	podmanFormatFlag     = "--format"
	podmanMarkerTarget   = "/tmp/devsy-engine-only-marker"
	podmanMarkerName     = "/marker"
	podmanHomeEnv        = "DEVSY_HOME"
)

type podmanMachineConnection struct {
	Name    string
	URI     string
	Default bool
}

type podmanMachineSSHConfig struct {
	Port           int
	RemoteUsername string
}

type podmanMachineInspection struct {
	Name      string
	State     string
	SSHConfig podmanMachineSSHConfig
}

type (
	podmanMachineName     struct{ Name string }
	podmanMachineSecurity struct{ Rootless bool }
	podmanMachineSocket   struct{ Path string }
	podmanMachineHost     struct {
		Security     podmanMachineSecurity
		Hostname     string
		RemoteSocket podmanMachineSocket
	}
)
type podmanMachineInfo struct{ Host podmanMachineHost }

type podmanBindFixture struct {
	Image  string   `json:"image"`
	Mounts []string `json:"mounts"`
}

var _ = ginkgo.Describe("Podman Machine engine-side bind sources",
	ginkgo.Label("up-provider-podman-machine-bind"), func() {
		ginkgo.It(
			"mounts a guest-only source without creating or changing it",
			func(ctx context.Context) {
				quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
				podman, err := exec.LookPath("podman")
				framework.ExpectNoError(
					err,
					"native gate requires an installed Podman CLI and running machine",
				)
				run := func(args ...string) string {
					// #nosec G204 -- executable is resolved Podman; arguments are test commands and quoted owned fixture paths.
					output, cmdErr := exec.CommandContext(ctx, podman, args...).CombinedOutput()
					framework.ExpectNoError(cmdErr, "podman %v: %s", args, output)
					return strings.TrimSpace(string(output))
				}
				for _, key := range []string{
					"CONTAINER_HOST", "CONTAINER_CONNECTION", "DOCKER_HOST", "DEVSY_CONFIG", "DEVSY_PROVIDER",
				} {
					gomega.Expect(os.Getenv(key)).
						To(gomega.BeEmpty(), "%s would override the isolated provider or selected connection", key)
				}
				var connections []podmanMachineConnection
				framework.ExpectNoError(
					json.Unmarshal(
						[]byte(
							run("system", "connection", "list", podmanFormatFlag, podmanJSONFormat),
						),
						&connections,
					),
				)
				selected := -1
				for i := range connections {
					if connections[i].Default {
						selected = i
					}
				}
				gomega.Expect(selected).
					To(gomega.BeNumerically(">=", 0), "a default native connection is required")
				connection := connections[selected]
				endpoint, err := url.Parse(connection.URI)
				framework.ExpectNoError(err)
				gomega.Expect(endpoint.Scheme).To(gomega.Equal(cmdSSH))
				gomega.Expect(endpoint.User).NotTo(gomega.BeNil())

				var machines []podmanMachineInspection
				var machineNames []podmanMachineName
				framework.ExpectNoError(
					json.Unmarshal(
						[]byte(
							run(podmanMachineCommand, "list", podmanFormatFlag, podmanJSONFormat),
						),
						&machineNames,
					),
				)
				inspectArgs := []string{podmanMachineCommand, "inspect"}
				for _, entry := range machineNames {
					inspectArgs = append(inspectArgs, entry.Name)
				}
				gomega.Expect(machineNames).NotTo(gomega.BeEmpty())
				framework.ExpectNoError(json.Unmarshal([]byte(run(inspectArgs...)), &machines))
				machine := ""
				for _, candidate := range machines {
					if candidate.State == "running" &&
						fmt.Sprint(candidate.SSHConfig.Port) == endpoint.Port() &&
						candidate.SSHConfig.RemoteUsername == endpoint.User.Username() {
						gomega.Expect(machine).
							To(gomega.BeEmpty(), "connection must identify exactly one machine")
						machine = candidate.Name
					}
				}
				gomega.Expect(machine).
					NotTo(gomega.BeEmpty(), "selected connection must match a running machine's SSH port and user")
				gomega.Expect(endpoint.Hostname()).
					To(gomega.BeElementOf("127.0.0.1", "localhost", "::1"))
				guest := func(script string) string { return run(podmanMachineCommand, cmdSSH, machine, "sh -c "+quote(script)) }
				var info podmanMachineInfo
				framework.ExpectNoError(
					json.Unmarshal([]byte(run("info", podmanFormatFlag, podmanJSONFormat)), &info),
				)
				gomega.Expect(info.Host.Security.Rootless).
					To(gomega.BeTrue(), "native gate requires rootless Podman")
				guestUID := guest("id -u")
				gomega.Expect(guestUID).NotTo(gomega.Equal("0"))
				ginkgo.GinkgoWriter.Printf(
					"Guest UID: %s; runtime directory: %s\n",
					guestUID,
					guest(`printf %s "$XDG_RUNTIME_DIR"`),
				)
				gomega.Expect(info.Host.Hostname).To(gomega.Equal(guest("hostname")))
				gomega.Expect(endpoint.Path).To(gomega.Equal(info.Host.RemoteSocket.Path))
				gomega.Expect(guest(`id -un`)).To(gomega.Equal(endpoint.User.Username()))
				ginkgo.GinkgoWriter.Printf(
					"Podman version: %s; selected connection: %s; machine: %s\n",
					run("version", podmanFormatFlag, podmanJSONFormat),
					connection.Name,
					machine,
				)

				source := guest(
					`set -eu; test -n "$XDG_RUNTIME_DIR"; test -d "$XDG_RUNTIME_DIR"; ` +
						`mktemp -d "$XDG_RUNTIME_DIR/devsy-bind-e2e.XXXXXXXX"`,
				)
				gomega.Expect(source).
					To(gomega.MatchRegexp(`^/[^\r\n']+/devsy-bind-e2e\.[A-Za-z0-9]+$`))
				ginkgo.DeferCleanup(func() {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					// #nosec G204 -- resolved Podman removes only the quoted directory created by this spec.
					output, cleanupErr := exec.CommandContext(cleanupCtx, podman, podmanMachineCommand, cmdSSH, machine,
						"sh -c "+quote("rm -rf -- "+quote(source))).
						CombinedOutput()
					framework.ExpectNoError(cleanupErr, "remove owned guest fixture: %s", output)
				})
				marker := filepath.Base(source)
				guest(
					"printf %s " + quote(
						marker,
					) + " > " + quote(
						source+podmanMarkerName,
					) + "; chmod 755 " + quote(
						source,
					) + "; chmod 666 " + quote(
						source+podmanMarkerName,
					),
				)
				_, err = os.Stat(source + podmanMarkerName)
				gomega.Expect(os.IsNotExist(err)).
					To(gomega.BeTrue(), "fixture must be absent on macOS: %s", source)
				gomega.Expect(guest("cat " + quote(source+podmanMarkerName))).
					To(gomega.Equal(marker))
				before := guest(
					"stat -c '%a:%u:%g:%s:%Y' " + quote(
						source,
					) + " " + quote(
						source+podmanMarkerName,
					),
				)

				initialDir, err := os.Getwd()
				framework.ExpectNoError(err)
				devsyHome, err := os.MkdirTemp("", "devsy-machine-bind-home-")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func() { framework.ExpectNoError(os.RemoveAll(devsyHome)) })
				previousHome, hadHome := os.LookupEnv(podmanHomeEnv)
				framework.ExpectNoError(os.Setenv(podmanHomeEnv, devsyHome))
				ginkgo.DeferCleanup(func() {
					if hadHome {
						framework.ExpectNoError(os.Setenv(podmanHomeEnv, previousHome))
					} else {
						framework.ExpectNoError(os.Unsetenv(podmanHomeEnv))
					}
				})
				f, err := framework.SetupDockerProvider(filepath.Join(initialDir, "bin"), podman)
				framework.ExpectNoError(err)
				content, err := json.Marshal(podmanBindFixture{
					Image: "ghcr.io/devsy-org/test-images/base:alpine",
					Mounts: []string{
						"type=bind,source=" + source + podmanMarkerName + ",target=" + podmanMarkerTarget + ",readonly",
					},
				})
				framework.ExpectNoError(err)
				workspace := framework.WriteTempDevcontainer("devsy-machine-bind-", string(content))
				ginkgo.DeferCleanup(func() { framework.ExpectNoError(os.RemoveAll(workspace)) })
				ginkgo.DeferCleanup(
					func(cleanupCtx context.Context) error { return f.CleanupWorkspace(cleanupCtx, workspace) },
				)
				sha, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
				framework.ExpectNoError(err)
				ginkgo.GinkgoWriter.Printf("Repository SHA: %s", sha)
				framework.ExpectNoError(f.DevsyUp(ctx, workspace))
				actual, err := f.DevsySSH(
					ctx,
					workspace,
					"if printf '' >> "+podmanMarkerTarget+"; then exit 1; fi; cat "+podmanMarkerTarget,
				)
				framework.ExpectNoError(err)
				gomega.Expect(actual).To(gomega.Equal(marker))
				framework.ExpectNoError(f.DevsyWorkspaceDelete(ctx, workspace))
				gomega.Expect(guest("cat " + quote(source+podmanMarkerName))).
					To(gomega.Equal(marker))
				gomega.Expect(guest("stat -c '%a:%u:%g:%s:%Y' " + quote(source) + " " + quote(source+podmanMarkerName))).
					To(gomega.Equal(before))
				_, err = os.Stat(source + podmanMarkerName)
				gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
				missingSource := source + "/missing-marker"
				invalidContent := strings.ReplaceAll(
					string(content),
					source+podmanMarkerName,
					missingSource,
				)
				framework.ExpectNoError(
					os.WriteFile(
						filepath.Join(workspace, ".devcontainer", "devcontainer.json"),
						[]byte(invalidContent),
						0o600,
					),
				)
				stdout, _, upErr := f.DevsyUpStreams(
					ctx,
					workspace,
					"--result-format",
					podmanJSONFormat,
				)
				gomega.Expect(upErr).To(gomega.HaveOccurred())
				envelope, ok := parseFailedStatus(stdout)
				gomega.Expect(ok).To(gomega.BeTrue(), "expected failed JSON envelope: %s", stdout)
				gomega.Expect(envelope.Error.Message).
					To(gomega.ContainSubstring("failed to start dev container"))
				gomega.Expect(envelope.Error.Message).To(gomega.ContainSubstring(missingSource))
				gomega.Expect(envelope.Error.Message).
					NotTo(gomega.ContainSubstring("bind mount source path does not exist"))
				guest("test ! -e " + quote(missingSource))
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)
	})
