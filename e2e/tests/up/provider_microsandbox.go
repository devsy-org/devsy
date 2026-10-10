package up

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	osLinux                      = "linux"
	microsandboxExternalProvider = "github.com/devsy-org/devsy-provider-microsandbox@v0.1.5"
	microsandboxRootUser         = "root"
)

func skipIfNoMicrosandbox(ctx context.Context) {
	checkMicrosandboxVersion(ctx)
	switch {
	case runtime.GOOS == osLinux:
		kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err != nil {
			microsandboxUnavailable("microsandbox requires KVM (/dev/kvm not accessible)")
		}
		_ = kvm.Close()
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		// Apple silicon supports microsandbox via the hypervisor framework.
	default:
		microsandboxUnavailable("microsandbox requires Apple silicon or Linux with KVM")
	}
}

func checkMicrosandboxVersion(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := exec.LookPath("msb"); err != nil {
		microsandboxUnavailable("microsandbox runtime (msb) not found on PATH")
	}
	output, err := exec.CommandContext(ctx, "msb", "--version").Output()
	if err != nil {
		microsandboxUnavailable("microsandbox runtime (msb) cannot execute: " + err.Error())
	}
	version, err := semver.ParseTolerant(
		strings.TrimPrefix(strings.TrimSpace(string(output)), "msb "),
	)
	if err != nil || version.LT(semver.Version{Major: 0, Minor: 7, Patch: 7}) {
		microsandboxUnavailable(
			"microsandbox parity requires msb v0.7.7 or newer: " + string(output),
		)
	}
}

func microsandboxUnavailable(reason string) {
	if os.Getenv("DEVSY_REQUIRE_MICROSANDBOX") == "true" {
		ginkgo.Fail(reason)
	}
	ginkgo.Skip(reason)
}

var _ = ginkgo.Describe(
	"testing up command for microsandbox provider",
	ginkgo.Label("up-provider-microsandbox"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func(ctx context.Context) {
			skipIfNoMicrosandbox(ctx)
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.DescribeTable("runs the shared lifecycle and ownership contract",
			func(ctx context.Context, source, name string) {
				ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
				ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
				f := framework.NewDefaultFramework(initialDir + "/bin")
				tempDir, err := framework.CopyToTempDir("tests/up/testdata/microsandbox")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				err = f.DevsyProviderAdd(ctx, source, "--name", name)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err := f.DevsyProviderDelete(cleanupCtx, name)
					framework.ExpectNoError(err)
				})
				ginkgo.DeferCleanup(f.CleanupWorkspace, tempDir)

				// full up: boots the microVM, streams in the agent, opens the tunnel
				err = f.DevsyUp(ctx, tempDir, "--devcontainer", ".devcontainer.json")
				framework.ExpectNoError(err)

				// the workspace is reachable over SSH
				err = f.DevsySSHEchoTestString(ctx, tempDir)
				framework.ExpectNoError(err)
				assertMicrosandboxSSHStreams(ctx, f, tempDir)

				workspacePath := filepath.Join("/workspaces", filepath.Base(tempDir))
				_, err = f.DevsySSHOnce(ctx, tempDir, fmt.Sprintf(
					"umask 022; : > %s/guest-created.txt && mkdir %s/guest-created-dir",
					workspacePath, workspacePath,
				))
				framework.ExpectNoError(err)
				for name, mode := range map[string]os.FileMode{"guest-created.txt": 0o644, "guest-created-dir": 0o755} {
					info, err := os.Stat(filepath.Join(tempDir, name))
					framework.ExpectNoError(err)
					gomega.Expect(info.Mode().Perm()).To(gomega.Equal(mode))
				}

				workspace, err := f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)
				sandbox := "devsy-" + devcontainer.GetRunnerIDFromWorkspace(workspace)
				createdAt := microsandboxCreationTime(ctx, sandbox)
				// No --user override: verify the runtime's default execution identity.
				// #nosec G204 -- fixed command and Devsy-generated sandbox name
				workloadUser, err := exec.CommandContext(
					ctx,
					"msb",
					"exec",
					"--stream",
					sandbox,
					"--",
					"id",
					"-u",
				).Output()
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(string(workloadUser))).To(gomega.Equal("0"))

				developer, err := f.DevsySSHOnce(ctx, tempDir, "id -un")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(developer)).To(gomega.Equal("vscode"))
				assertMicrosandboxHostEntries(ctx, f, tempDir, "fresh")
				err = f.DevsyWorkspaceStop(ctx, tempDir)
				framework.ExpectNoError(err)
				err = f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)
				gomega.Expect(microsandboxCreationTime(ctx, sandbox)).To(gomega.Equal(createdAt))
				assertMicrosandboxHostEntries(ctx, f, tempDir, "restarted")
				err = f.DevsyUpRecreate(ctx, tempDir)
				framework.ExpectNoError(err)
				gomega.Expect(microsandboxCreationTime(ctx, sandbox)).NotTo(gomega.Equal(createdAt))
				assertMicrosandboxHostEntries(ctx, f, tempDir, "recreated")

				previousCreation := microsandboxCreationTime(ctx, sandbox)
				// #nosec G204 -- fixed command and Devsy-generated sandbox name
				_, err = exec.CommandContext(
					ctx, "msb", "exec", "--stream", sandbox, "--", "sh", "-c",
					"echo preserved > /root/recreation-preserved.txt",
				).
					Output()
				framework.ExpectNoError(err)

				configPath := filepath.Join(tempDir, ".devcontainer.json")
				// #nosec G304 -- configuration copied into the test-owned temporary directory
				data, err := os.ReadFile(configPath)
				framework.ExpectNoError(err)
				var devConfig map[string]any
				framework.ExpectNoError(json.Unmarshal(data, &devConfig))
				devConfig["remoteUser"] = microsandboxRootUser
				data, err = json.Marshal(devConfig)
				framework.ExpectNoError(err)
				framework.ExpectNoError(os.WriteFile(configPath, data, 0o600))

				stdout, stderr, err := f.DevsyUpStreams(
					ctx, tempDir, "--devcontainer", ".devcontainer.json",
				)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(stdout + stderr).To(gomega.ContainSubstring("--recreate"))
				gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
					To(gomega.Equal(previousCreation))
				// #nosec G204 -- fixed command and Devsy-generated sandbox name
				preserved, err := exec.CommandContext(
					ctx,
					"msb",
					"exec",
					"--stream",
					sandbox,
					"--",
					"cat",
					"/root/recreation-preserved.txt",
				).
					Output()
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(string(preserved))).To(gomega.Equal("preserved"))
				err = f.DevsyUp(ctx, tempDir, "--devcontainer", ".devcontainer.json", "--recreate")
				framework.ExpectNoError(err)
				gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
					NotTo(gomega.Equal(previousCreation))
				developer, err = f.DevsySSHOnce(ctx, tempDir, "id -u")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(developer)).To(gomega.Equal("0"))
				assertMicrosandboxHostEntries(ctx, f, tempDir, "identity-changed")
				framework.ExpectNoError(f.DevsyWorkspaceDelete(ctx, tempDir))
				// #nosec G204 -- fixed command and test-owned sandbox name
				output, err := exec.CommandContext(ctx, "msb", "list", "--format", "json").Output()
				framework.ExpectNoError(err)
				gomega.Expect(string(output)).NotTo(gomega.ContainSubstring(sandbox))
			},
			ginkgo.Entry("built-in", "microsandbox", "microsandbox-builtin-parity",
				ginkgo.SpecTimeout(framework.TimeoutLong())),
			ginkgo.Entry(
				"external v0.1.5",
				microsandboxExternalProvider,
				"microsandbox-external-parity",
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
		)
	},
)

func assertMicrosandboxSSHStreams(ctx context.Context, f *framework.Framework, workspace string) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	payload := bytes.Repeat([]byte{0, 1, 10, 13, 27, 127, 128, 255}, 128*1024)
	var stdout, stderr bytes.Buffer
	// #nosec G204 -- test binary, test-owned workspace, and fixed guest command
	command := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName),
		"workspace", "ssh", workspace, "--command", "cat; printf parity-stderr >&2; exit 23")
	docker.PrepareForGroupCancellation(command)
	command.WaitDelay = 30 * time.Second
	command.Stdin = bytes.NewReader(payload)
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	var exit *exec.ExitError
	gomega.Expect(errors.As(err, &exit)).
		To(gomega.BeTrue(), "expected guest exit 23: %v; stderr: %s", err, stderr.String())
	gomega.Expect(exit.ExitCode()).To(gomega.Equal(23))
	gomega.Expect(stdout.Bytes()).To(gomega.Equal(payload))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("parity-stderr"))
}

// Entries are created after setup so recursive workspace chown cannot mask fallback ownership.
func assertMicrosandboxHostEntries(
	ctx context.Context,
	f *framework.Framework,
	hostPath, prefix string,
) {
	guestPath := filepath.Join("/workspaces", filepath.Base(hostPath))
	identity, err := f.DevsySSHOnce(ctx, hostPath, `printf '%s:%s' "$(id -u)" "$(id -g)"`)
	framework.ExpectNoError(err)
	identity = strings.TrimSpace(identity)

	for name, mode := range map[string]os.FileMode{prefix + "-file": 0o644, prefix + "-dir": 0o755} {
		hostEntry := filepath.Join(hostPath, name)
		if mode == 0o644 {
			//nolint:gosec // permission mirroring is the behavior under test
			err = os.WriteFile(hostEntry, []byte("host"), mode)
		} else {
			err = os.Mkdir(hostEntry, mode)
		}
		framework.ExpectNoError(err)
		//nolint:gosec // normalize host umask before checking guest-visible modes
		framework.ExpectNoError(os.Chmod(hostEntry, mode))
		ownerBefore := microsandboxHostOwner(ctx, hostEntry)
		result, err := f.DevsySSHOnce(
			ctx,
			hostPath,
			fmt.Sprintf("stat -c '%%u:%%g %%a' %s/%s", guestPath, name),
		)
		framework.ExpectNoError(err)
		gomega.Expect(strings.TrimSpace(result)).
			To(gomega.Equal(fmt.Sprintf("%s %o", identity, mode)))
		gomega.Expect(microsandboxHostOwner(ctx, hostEntry)).To(gomega.Equal(ownerBefore))
	}
}

func microsandboxHostOwner(ctx context.Context, entry string) string {
	args := []string{"-c", "%u:%g", entry}
	if runtime.GOOS == "darwin" {
		args = []string{"-f", "%u:%g", entry}
	}
	// #nosec G204 -- fixed stat executable and test-created path
	output, err := exec.CommandContext(ctx, "stat", args...).Output()
	framework.ExpectNoError(err)
	return strings.TrimSpace(string(output))
}

func microsandboxCreationTime(ctx context.Context, sandbox string) string {
	// #nosec G204 -- fixed inspect command and Devsy-generated sandbox name
	output, err := exec.CommandContext(ctx, "msb", "inspect", sandbox, "--format", "json").Output()
	framework.ExpectNoError(err)
	var info struct {
		CreatedAt string `json:"created_at"`
	}
	framework.ExpectNoError(json.Unmarshal(output, &info))
	gomega.Expect(info.CreatedAt).NotTo(gomega.BeEmpty())
	return info.CreatedAt
}
