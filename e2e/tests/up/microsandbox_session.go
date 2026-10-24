package up

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/onsi/ginkgo/v2"
)

const (
	microsandboxResourceBuiltin  = provider.DeprecatedMicrosandboxSource
	osLinux                      = "linux"
	microsandboxExternalProvider = "github.com/devsy-org/devsy-provider-microsandbox@v0.1.10"
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

type microsandboxProviderCase struct {
	name, source, runtime, label string
}

func microsandboxProviders(builtinName, externalName string) []microsandboxProviderCase {
	return []microsandboxProviderCase{
		{
			name:    builtinName,
			source:  microsandboxResourceBuiltin,
			runtime: provider.MicrosandboxDriver,
			label:   "microsandbox-builtin",
		},
		{
			name:    externalName,
			source:  microsandboxExternalProvider,
			runtime: provider.ExternalDriver,
			label:   "microsandbox-external",
		},
	}
}

type microsandboxTestSession struct {
	framework  *framework.Framework
	initialDir string
}

func newMicrosandboxTestSession(ctx context.Context) *microsandboxTestSession {
	skipIfNoMicrosandbox(ctx)
	ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
	ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
	initialDir, err := os.Getwd()
	framework.ExpectNoError(err)
	return &microsandboxTestSession{
		framework:  framework.NewDefaultFramework(filepath.Join(initialDir, "bin")),
		initialDir: initialDir,
	}
}

func (s *microsandboxTestSession) newWorkspace() string {
	workspace, err := framework.CreateTempDir()
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, s.initialDir, workspace)
	return workspace
}

func (s *microsandboxTestSession) copyWorkspace(source string) string {
	return framework.CopyTestdata(source, s.initialDir)
}

func (s *microsandboxTestSession) addProvider(ctx context.Context, source, name string) {
	framework.ExpectNoError(s.framework.DevsyProviderAdd(ctx, source, "--name", name))
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := framework.DetachedCleanupContext(cleanupCtx, time.Minute)
		defer cancel()
		return s.framework.DevsyProviderDelete(cleanupCtx, name)
	})
}

func (s *microsandboxTestSession) cleanupWorkspace(workspace string) {
	ginkgo.DeferCleanup(s.framework.CleanupWorkspace, workspace)
}
