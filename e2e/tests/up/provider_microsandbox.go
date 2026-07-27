package up

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
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
			err = f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

			// the workspace is reachable over SSH
			err = f.DevsySSHEchoTestString(ctx, tempDir)
			framework.ExpectNoError(err)

			// stop, then bring it back up and confirm it is reachable again
			err = f.DevsyWorkspaceStop(ctx, tempDir)
			framework.ExpectNoError(err)

			err = f.DevsyUp(ctx, tempDir)
			framework.ExpectNoError(err)

			err = f.DevsySSHEchoTestString(ctx, tempDir)
			framework.ExpectNoError(err)
		}, ginkgo.SpecTimeout(framework.TimeoutModerate()))
	},
)
