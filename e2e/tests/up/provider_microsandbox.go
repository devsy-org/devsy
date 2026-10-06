package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const osLinux = "linux"

// skipIfNoMicrosandbox skips when the microsandbox runtime or hardware
// virtualization is unavailable, mirroring how other providers guard on their
// runtime being present.
func skipIfNoMicrosandbox() {
	if _, err := exec.LookPath("msb"); err != nil {
		ginkgo.Skip("microsandbox runtime (msb) not found on PATH")
	}
	switch {
	case runtime.GOOS == osLinux:
		kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err != nil {
			ginkgo.Skip("microsandbox requires KVM (/dev/kvm not accessible)")
		}
		_ = kvm.Close()
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		// Apple silicon supports microsandbox via the hypervisor framework.
	default:
		ginkgo.Skip("microsandbox requires Apple silicon or Linux with KVM")
	}
}

var _ = ginkgo.Describe(
	"testing up command for microsandbox provider",
	ginkgo.Label("up-provider-microsandbox"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			skipIfNoMicrosandbox()
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("runs devsy in a microsandbox microVM", func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			tempDir, err := framework.CopyToTempDir("tests/up/testdata/microsandbox")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			_ = f.DevsyProviderDelete(ctx, "microsandbox")
			err = f.DevsyProviderAdd(ctx, "microsandbox")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
				err := f.DevsyProviderDelete(cleanupCtx, "microsandbox")
				framework.ExpectNoError(err)
			})

			// full up: boots the microVM, streams in the agent, opens the tunnel
			err = f.DevsyUp(ctx, tempDir, "--devcontainer", ".devcontainer.json")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			// the workspace is reachable over SSH
			err = f.DevsySSHEchoTestString(ctx, tempDir)
			framework.ExpectNoError(err)

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
			configPath := filepath.Join(tempDir, ".devcontainer.json")
			// #nosec G304 -- configuration copied into the test-owned temporary directory
			data, err := os.ReadFile(configPath)
			framework.ExpectNoError(err)
			var devConfig map[string]any
			framework.ExpectNoError(json.Unmarshal(data, &devConfig))
			devConfig["remoteUser"] = "root"
			data, err = json.Marshal(devConfig)
			framework.ExpectNoError(err)
			framework.ExpectNoError(os.WriteFile(configPath, data, 0o600))
			err = f.DevsyWorkspaceStop(ctx, tempDir)
			framework.ExpectNoError(err)
			err = f.DevsyUp(ctx, tempDir, "--devcontainer", ".devcontainer.json")
			framework.ExpectNoError(err)
			gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
				NotTo(gomega.Equal(previousCreation))
			developer, err = f.DevsySSHOnce(ctx, tempDir, "id -u")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(developer)).To(gomega.Equal("0"))
			assertMicrosandboxHostEntries(ctx, f, tempDir, "identity-changed")
		}, ginkgo.SpecTimeout(framework.TimeoutModerate()))
	},
)

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
