//go:build linux

package up

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

type remoteAgentArtifactServer struct {
	server          *http.Server
	listener        net.Listener
	done            chan error
	runtimeRequests atomic.Int32
	agentRequests   atomic.Int32
	offline         atomic.Bool
	runtime         []byte
	agent           []byte
}

const (
	remoteAgentDockerRunFlag   = "run"
	remoteAgentDockerMountFlag = "--mount"
	remoteAgentDockerEnvFlag   = "--env"
)

var _ = ginkgo.Describe("published MicroSandbox provider remote agent binaries",
	ginkgo.Label("up-provider-microsandbox-remote-agent-binaries", "microsandbox-external"),
	func() {
		ginkgo.It(
			"downloads the external runtime on the remote agent and reuses its workspace copy",
			func(ctx ginkgo.SpecContext) {
				session := newMicrosandboxTestSession(ctx)
				framework.ExpectNoError(requireRemoteAgentHostPrerequisites())

				providerName := "case-a-remote-agent"
				stageProviderName := "case-a-remote-stage"
				session.addProvider(ctx, microsandboxExternalProvider, stageProviderName)
				staged := prepareMicrosandboxConsumerProvider(ctx, stageProviderName)
				runtimeBytes, err := os.ReadFile(staged.runtimeBinary)
				framework.ExpectNoError(err)
				runtimeRelativePath, err := remoteAgentRuntimeRelativePath(staged)
				framework.ExpectNoError(err)
				descriptor, err := provider.LoadProviderConfig(
					config.DefaultContext,
					stageProviderName,
				)
				framework.ExpectNoError(err)

				containerName := "devsy-" + microsandboxConsumerID("agent")
				agentHome := filepath.Join(
					"/tmp/devsy-remote-agent-fixture",
					microsandboxConsumerID("home"),
					"agent",
				)
				bundleRoot, err := remoteAgentBundleRoot()
				framework.ExpectNoError(err)
				dockerPath, err := exec.LookPath("docker")
				framework.ExpectNoError(err)
				dockerPath, err = filepath.Abs(dockerPath)
				framework.ExpectNoError(err)
				preflightRemoteAgentContainer(ctx, bundleRoot, dockerPath, filepath.Dir(agentHome))

				binaryName := descriptor.Agent.External.Binary
				checksums := descriptor.Agent.Binaries[binaryName]
				binaryEntry := remoteAgentLinuxBinary(checksums)
				descriptorHash := strings.TrimPrefix(
					strings.ToLower(binaryEntry.Checksum),
					"sha256:",
				)
				gomega.Expect(descriptorHash).To(gomega.Equal(staged.runtimeHash))
				agentPath := filepath.Join(
					session.framework.DevsyBinDir,
					session.framework.DevsyBinName,
				)
				server := startRemoteAgentArtifactServer(runtimeBytes, agentPath)
				ginkgo.DeferCleanup(func(cleanupCtx ginkgo.SpecContext) error {
					closeCtx, cancel := framework.DetachedCleanupContext(cleanupCtx, 10*time.Second)
					defer cancel()
					return server.close(closeCtx)
				})

				descriptor.Name = providerName
				descriptor.Agent.Local = types.StrBool("false")
				descriptor.Agent.DataPath = agentHome
				descriptor.Agent.DownloadURL = server.agentURL()
				binaryEntry.Path = server.runtimeURL()
				descriptor.Agent.Binaries[binaryName] = checksums
				descriptor.Exec = remoteAgentCommands(
					containerName,
					bundleRoot,
					dockerPath,
					agentHome,
				)
				configPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "provider.yaml")
				configBytes, err := yaml.Marshal(descriptor)
				framework.ExpectNoError(err)
				framework.ExpectNoError(os.WriteFile(configPath, configBytes, 0o600))
				session.addProvider(ctx, configPath, providerName)
				framework.ExpectNoError(session.framework.DevsyProviderUse(ctx, providerName))

				workspace := session.newWorkspace()
				ginkgo.DeferCleanup(func(cleanupCtx ginkgo.SpecContext) error {
					boundedCtx, cancel := framework.DetachedCleanupContext(
						cleanupCtx,
						20*time.Second,
					)
					defer cancel()
					return removeRemoteAgentContainer(boundedCtx, containerName)
				})
				ginkgo.DeferCleanup(session.framework.CleanupWorkspace, workspace)
				framework.ExpectNoError(session.framework.DevsyUp(ctx, workspace))
				stdout, stderr, err := captureWorkspaceExec(
					ctx, session.framework, workspace,
					microsandboxExecRequest{Args: []string{"--", "printf", "remote-agent-exec-ok"}},
				)
				framework.ExpectNoError(err, stderr)
				gomega.Expect(stdout).To(gomega.Equal("remote-agent-exec-ok"),
					"workspace exec must reach the guest through the remote agent")
				gomega.Expect(server.runtimeRequests.Load()).To(gomega.Equal(int32(1)),
					"remote agent must fetch the published provider binary exactly once")
				gomega.Expect(server.agentRequests.Load()).To(gomega.BeNumerically(">", 0),
					"remote agent must fetch its agent binary from the fixture endpoint")
				workspaceRecord, err := session.framework.FindWorkspace(ctx, workspace)
				framework.ExpectNoError(err)
				remoteBinaryPath := filepath.Join(
					agentHome, "contexts", config.DefaultContext, "workspaces", workspaceRecord.ID,
					"binaries", runtimeRelativePath,
				)
				firstBinary := assertRemoteAgentBinary(
					ctx, containerName, remoteBinaryPath, staged.runtimeHash,
				)
				assertRemoteAgentGuest(ctx, session.framework, workspace)

				clearRemoteAgentBinaryCache(ctx, containerName, filepath.Dir(agentHome))
				framework.ExpectNoError(session.framework.DevsyStop(ctx, workspace))
				server.offline.Store(true)
				framework.ExpectNoError(session.framework.DevsyUp(ctx, workspace))
				gomega.Expect(server.runtimeRequests.Load()).To(gomega.Equal(int32(1)),
					"existing workspace binary must be reused when its source returns 404")
				secondBinary := assertRemoteAgentBinary(
					ctx, containerName, remoteBinaryPath, staged.runtimeHash,
				)
				gomega.Expect(secondBinary).To(
					gomega.Equal(firstBinary),
					"workspace binary identity must survive stop and restart",
				)
				assertRemoteAgentGuest(ctx, session.framework, workspace)
			}, ginkgo.SpecTimeout(framework.TimeoutLong()),
		)
	},
)

func remoteAgentRuntimeRelativePath(staged microsandboxConsumerProvider) (string, error) {
	binaryRoot := filepath.Join(staged.origin, "binaries")
	relative, err := filepath.Rel(binaryRoot, staged.runtimeBinary)
	if err != nil {
		return "", fmt.Errorf("resolve staged provider binary path: %w", err)
	}
	if relative == "." || filepath.IsAbs(relative) || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"staged provider binary path escapes its binaries directory: %q", relative,
		)
	}
	return relative, nil
}

func remoteAgentLinuxBinary(entries []*provider.ProviderBinary) *provider.ProviderBinary {
	for _, entry := range entries {
		if entry.OS == runtime.GOOS && entry.Arch == runtime.GOARCH {
			return entry
		}
	}
	ginkgo.Fail("published MicroSandbox descriptor has no Linux/amd64 runtime binary")
	return nil
}

func startRemoteAgentArtifactServer(
	runtimeBinary []byte,
	agentPath string,
) *remoteAgentArtifactServer {
	// #nosec G304 -- agentPath is built from Framework.DevsyBinDir and its configured binary name.
	agentBinary, err := os.ReadFile(agentPath)
	framework.ExpectNoError(err)
	// #nosec G102 -- Docker bridge containers need the ephemeral host port; cleanup closes this test server.
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	framework.ExpectNoError(err)
	server := &remoteAgentArtifactServer{
		listener: listener, runtime: runtimeBinary, done: make(chan error, 1),
	}
	server.agent = agentBinary
	server.server = &http.Server{
		Handler:           server.artifactHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       30 * time.Second,
	}
	go func() { server.done <- server.server.Serve(listener) }()
	return server
}

func (s *remoteAgentArtifactServer) artifactHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/agent/devsy-linux-amd64", s.serveAgent)
	mux.HandleFunc("/provider/runtime", s.serveRuntime)
	return mux
}

func (s *remoteAgentArtifactServer) serveAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.agentRequests.Add(1)
	_, _ = w.Write(s.agent)
}

func (s *remoteAgentArtifactServer) serveRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.runtimeRequests.Add(1)
	if s.offline.Load() {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(s.runtime)
}

func (s *remoteAgentArtifactServer) baseURL() string {
	address, err := remoteAgentArtifactBaseURL(s.listener.Addr().String())
	framework.ExpectNoError(err)
	return address
}

func remoteAgentArtifactBaseURL(address string) (string, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("parse remote agent artifact listener: %w", err)
	}
	return "http://" + net.JoinHostPort("host.docker.internal", port), nil
}

func (s *remoteAgentArtifactServer) runtimeURL() string { return s.baseURL() + "/provider/runtime" }
func (s *remoteAgentArtifactServer) agentURL() string   { return s.baseURL() + "/agent" }

func (s *remoteAgentArtifactServer) close(ctx context.Context) error {
	shutdownErr := s.server.Shutdown(ctx)
	closeErr := s.server.Close()
	select {
	case serveErr := <-s.done:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, closeErr, serveErr)
	case <-ctx.Done():
		return errors.Join(shutdownErr, closeErr, ctx.Err())
	}
}

func remoteAgentCommands(
	containerName, bundleRoot, dockerPath, agentHome string,
) provider.ProviderCommands {
	devsyHome := filepath.Dir(agentHome)
	create := remoteAgentContainerCreateCommand(containerName, bundleRoot, dockerPath, devsyHome)
	return provider.ProviderCommands{
		Create: types.StrArray{
			create,
		},
		Command: types.StrArray{
			fmt.Sprintf("docker exec -i %s sh -c \"${COMMAND}\"", containerName),
		},
		Stop: types.StrArray{
			"docker stop " + shellescape.Quote(containerName),
		},
		Start: types.StrArray{
			"docker start " + shellescape.Quote(containerName),
		},
		Status: types.StrArray{
			fmt.Sprintf(
				`state=$(docker inspect -f '{{.State.Status}}' %s 2>/dev/null) || { echo NOTFOUND; exit 0; }
case "$state" in
  running) echo RUNNING ;;
  exited) echo STOPPED ;;
  *) echo BUSY ;;
esac`,
				containerName,
			),
		},
		Delete: types.StrArray{
			"docker rm -f " + shellescape.Quote(containerName),
		},
	}
}

const (
	remoteAgentContainerImage = "ubuntu:24.04"
	remoteAgentContainerInit  = `apt-get update &&
apt-get install -y --no-install-recommends ca-certificates curl &&
rm -rf /var/lib/apt/lists/* &&
touch /tmp/devsy-agent-ready &&
sleep infinity`
	remoteAgentContainerReady = `test -e /tmp/devsy-agent-ready &&
test "$(uname -s)" = Linux &&
test "$(uname -m)" = x86_64 &&
grep -Eq '^(ID=ubuntu|ID=debian)$' /etc/os-release &&
test -c /dev/kvm &&
test -r /dev/kvm &&
test -w /dev/kvm &&
command -v curl >/dev/null &&
test -s /etc/ssl/certs/ca-certificates.crt &&
/opt/devsy-microsandbox/bin/msb --version &&
/usr/local/bin/docker version >/dev/null`
)

func remoteAgentContainerArgs(name, bundleRoot, dockerPath, devsyHome string) []string {
	return []string{
		remoteAgentDockerRunFlag,
		"-d",
		"--name",
		name,
		"--add-host=host.docker.internal:host-gateway",
		"--device=/dev/kvm:/dev/kvm",
		remoteAgentDockerMountFlag,
		"type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock",
		remoteAgentDockerMountFlag,
		"type=bind,src=" + bundleRoot + ",dst=/opt/devsy-microsandbox,readonly",
		remoteAgentDockerMountFlag,
		"type=bind,src=" + dockerPath + ",dst=/usr/local/bin/docker,readonly",
		remoteAgentDockerEnvFlag,
		"HOME=/root",
		remoteAgentDockerEnvFlag,
		"DEVSY_HOME=" + devsyHome,
		remoteAgentDockerEnvFlag,
		"PATH=/opt/devsy-microsandbox/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		remoteAgentDockerEnvFlag,
		"LD_LIBRARY_PATH=/opt/devsy-microsandbox/lib",
		remoteAgentContainerImage,
		"sh",
		"-c",
		remoteAgentContainerInit,
	}
}

func remoteAgentContainerCreateCommand(name, bundleRoot, dockerPath, devsyHome string) string {
	args := remoteAgentContainerArgs(name, bundleRoot, dockerPath, devsyHome)
	quotedArgs := make([]string, len(args))
	for i, arg := range args {
		quotedArgs[i] = shellescape.Quote(arg)
	}
	waitForSetup := fmt.Sprintf(`i=0
while [ "$i" -lt 180 ]; do
  if docker exec %s test -e /tmp/devsy-agent-ready; then break; fi
  i=$((i + 1))
  sleep 1
done`, shellescape.Quote(name))
	checkReady := "docker exec " + shellescape.Quote(name) +
		" sh -c " + shellescape.Quote(remoteAgentContainerReady)
	return strings.Join([]string{
		"docker " + strings.Join(quotedArgs, " "),
		"&& { " + waitForSetup + "; }",
		"&& " + checkReady,
	}, " ")
}

func requireRemoteAgentHostPrerequisites() error {
	if runtime.GOOS != osLinux || runtime.GOARCH != "amd64" {
		return fmt.Errorf("remote MicroSandbox agent proof requires Linux/amd64")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		return fmt.Errorf("remote MicroSandbox agent proof requires host /dev/kvm: %w", err)
	}
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		return fmt.Errorf("remote MicroSandbox agent proof requires Docker socket: %w", err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return err
	}
	return nil
}

func remoteAgentBundleRoot() (string, error) {
	msb, err := exec.LookPath("msb")
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(msb)
	if err != nil {
		return "", err
	}
	if filepath.Base(filepath.Dir(resolved)) != "bin" ||
		filepath.Base(filepath.Dir(filepath.Dir(resolved))) != ".microsandbox" {
		return "", fmt.Errorf("msb is not in the pinned .microsandbox bundle: %s", resolved)
	}
	return filepath.Dir(filepath.Dir(resolved)), nil
}

func preflightRemoteAgentContainer(ctx context.Context, bundleRoot, dockerPath, devsyHome string) {
	name := "devsy-agent-preflight-" + microsandboxConsumerID("container")
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := removeRemoteAgentContainer(cleanupCtx, name); err != nil {
			ginkgo.Fail("remove remote agent preflight container: " + err.Error())
		}
	}()
	cmdCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	args := remoteAgentContainerArgs(name, bundleRoot, dockerPath, devsyHome)
	// #nosec G204 -- args are fixed by the builder and use test-owned paths.
	output, err := exec.CommandContext(cmdCtx, "docker", args...).CombinedOutput()
	framework.ExpectNoError(err, string(output))
	framework.ExpectNoError(waitRemoteAgentContainer(cmdCtx, dockerPath, name))
	// #nosec G204 -- script is fixed and name is a generated fixture nonce.
	preflightOutput, err := exec.CommandContext(
		cmdCtx, "docker", "exec", name, "sh", "-c", remoteAgentContainerReady,
	).CombinedOutput()
	framework.ExpectNoError(err, "remote agent container prerequisites: "+string(preflightOutput))
}

func waitRemoteAgentContainer(ctx context.Context, dockerPath, name string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// #nosec G204 -- dockerPath is from exec.LookPath; arguments are fixed or nonce-generated.
		cmd := exec.CommandContext(
			ctx, dockerPath, "exec", name, "test", "-e", "/tmp/devsy-agent-ready",
		)
		if err := cmd.Run(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for remote agent container setup: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func removeRemoteAgentContainer(ctx context.Context, name string) error {
	// #nosec G204 -- docker is fixed; name is a nonce generated within this test.
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", name)
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "No such container") {
		return fmt.Errorf("remove remote agent container: %w: %s", err, output)
	}
	return nil
}

func assertRemoteAgentBinary(ctx context.Context, containerName, path, expectedHash string) string {
	command := fmt.Sprintf(
		"test -x %s && stat -c '%%a %%i' %s && sha256sum %s",
		shellescape.Quote(path), shellescape.Quote(path), shellescape.Quote(path),
	)
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// #nosec G204 -- docker is fixed and command uses shell-quoted test-owned paths.
	output, err := exec.CommandContext(
		cmdCtx, "docker", "exec", containerName, "sh", "-c", command,
	).CombinedOutput()
	framework.ExpectNoError(err, string(output))
	fields := strings.Fields(string(output))
	gomega.Expect(fields).To(gomega.HaveLen(4))
	gomega.Expect(fields[0]).To(gomega.Equal("755"))
	gomega.Expect(fields[2]).To(gomega.Equal(expectedHash))
	return fields[1]
}

func clearRemoteAgentBinaryCache(ctx context.Context, containerName, devsyHome string) {
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cacheDir := filepath.Join(devsyHome, "cache", "providers")
	command := "rm -rf " + shellescape.Quote(cacheDir) +
		" && test ! -e " + shellescape.Quote(cacheDir)
	// #nosec G204 -- command removes only the nonce-scoped private provider cache.
	output, err := exec.CommandContext(
		cmdCtx, "docker", "exec", containerName, "sh", "-c", command,
	).CombinedOutput()
	framework.ExpectNoError(err, string(output))
}

func assertRemoteAgentGuest(ctx context.Context, f *framework.Framework, workspace string) {
	marker := "remote-agent-" + microsandboxConsumerID("guest")
	out, err := f.DevsySSHOnce(ctx, workspace, "printf %s "+shellescape.Quote(marker))
	framework.ExpectNoError(err)
	gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal(marker))
}
