//go:build linux

package up

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const safeRecreateManagedConflictErr = "tmpfs root disk size (1024 MiB) must not exceed sandbox memory (512 MiB)"

var _ = ginkgo.Describe("MicroSandbox managed-policy rejected replacement",
	ginkgo.Label("up-provider-microsandbox-managed-recreate"), func() {
		ginkgo.DescribeTable(
			"keeps the original generation until the managed-policy conflict is removed",
			func(ctx context.Context, source, name string, stopped bool) {
				requireManagedRecreateFixture()
				fixture := newSafeRecreateFixture(ctx, source, name, 2048)
				fixture.initialize(ctx, stopped)
				ginkgo.DeferCleanup(func(context.Context) error {
					return clearSafeRecreateManagedPolicy()
				})

				// Prove an empty managed policy works before adding the conflicting override.
				framework.ExpectNoError(clearSafeRecreateManagedPolicy())
				framework.ExpectNoError(fixture.useManagedRecreateOptions(ctx, false))
				fixture.writeConfig("2gb")
				framework.ExpectNoError(setSafeRecreateManagedMemory512())
				out, err := microsandboxResourceCommand(ctx, "list", "--quiet")
				framework.ExpectNoError(err, string(out))
				gomega.Expect(strings.Fields(string(out))).
					To(gomega.ContainElement(fixture.sandbox))

				stdout, stderr, err := fixture.framework.DevsyUpStreams(
					ctx,
					fixture.workspace,
					append(fixture.upArgs(), "--recreate")...,
				)
				gomega.Expect(err).
					To(gomega.HaveOccurred(), "stdout: %s stderr: %s", stdout, stderr)
				gomega.Expect(stdout+stderr).To(
					gomega.ContainSubstring(safeRecreateManagedConflictErr),
					"request must reach final managed-layer tmpfs validation",
				)
				// Inspect status and host state before any command can start a stopped VM.
				gomega.Expect(inspectSafeRecreate(ctx, fixture.sandbox)).
					To(gomega.Equal(fixture.original))
				fixture.assertHostState(ctx)

				// Restore policy and original provider options before resuming stopped originals.
				framework.ExpectNoError(clearSafeRecreateManagedPolicy())
				framework.ExpectNoError(fixture.useManagedRecreateOptions(ctx, true))
				fixture.inspectOriginalFiles(ctx, stopped)

				// The exact same request is valid without managed policy and must replace.
				framework.ExpectNoError(fixture.useManagedRecreateOptions(ctx, false))
				fixture.writeConfig("2gb")
				framework.ExpectNoError(fixture.framework.DevsyUpRecreate(
					ctx, append([]string{fixture.workspace}, fixture.upArgs()...)...,
				))
				after := inspectSafeRecreate(ctx, fixture.sandbox)
				gomega.Expect(after.Status).To(gomega.Equal("Running"))
				gomega.Expect(after.CreatedAt).NotTo(gomega.Equal(fixture.original.CreatedAt))
				gomega.Expect(microsandboxStorageBootID(ctx, fixture.framework, fixture.workspace)).
					NotTo(gomega.Equal(fixture.bootID))
				out, err = safeRecreateExec(ctx, fixture.sandbox, "sh", "-c",
					`test ! -e "$1"`, "sh", safeRecreateRootPath)
				framework.ExpectNoError(err, string(out))
				fixture.assertPersistentMounts(ctx)
				fixture.assertHostState(ctx)
				expected := microsandboxResources{
					CPUs: 1, MemoryMiB: 2048, MaxCPUs: 1, MaxMemoryMiB: 2048,
				}
				gomega.Expect(microsandboxActiveResources(ctx, fixture.sandbox)).
					To(gomega.Equal(expected))
				assertMicrosandboxGuestResources(
					ctx,
					fixture.framework,
					fixture.workspace,
					expected,
				)
			},
			ginkgo.Entry(
				"builtin running",
				microsandboxResourceBuiltin,
				"safe-recreate-managed-builtin-running",
				false,
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"builtin stopped",
				microsandboxResourceBuiltin,
				"safe-recreate-managed-builtin-stopped",
				true,
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"external running",
				microsandboxExternalProvider,
				"safe-recreate-managed-external-running",
				false,
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"external stopped",
				microsandboxExternalProvider,
				"safe-recreate-managed-external-stopped",
				true,
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
		)
	})

func requireManagedRecreateFixture() {
	required := os.Getenv("DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE") == safeRecreateEnabledValue
	marker := os.Getenv("DEVSY_TEST_MSB_MANAGED_POLICY")
	if required {
		gomega.Expect(marker).
			To(
				gomega.Equal(safeRecreateManagedPolicyPath),
				"required managed-policy fixture wrapper did not provide its isolated policy path",
			)
		return
	}
	if marker == "" {
		ginkgo.Skip("requires the isolated managed-policy E2E wrapper")
	}
	gomega.Expect(marker).
		To(gomega.Equal(safeRecreateManagedPolicyPath), "managed-policy fixture marker has unexpected path")
}

func (f *safeRecreateFixture) useManagedRecreateOptions(
	ctx context.Context,
	originalOptions bool,
) error {
	storage := 1
	ephemeral := safeRecreateEnabledValue
	if originalOptions {
		storage = 2
		ephemeral = "false"
	}
	return f.framework.DevsyProviderUse(ctx, f.provider,
		microsandboxResourceOption, "MICROSANDBOX_MEMORY=0",
		microsandboxResourceOption, fmt.Sprintf("MICROSANDBOX_MAX_MEMORY=%d", f.maxMemoryMiB),
		microsandboxResourceOption, "MICROSANDBOX_CPUS=1",
		microsandboxResourceOption, "MICROSANDBOX_MAX_CPUS=1",
		microsandboxResourceOption, fmt.Sprintf("MICROSANDBOX_STORAGE=%d", storage),
		microsandboxResourceOption, "MICROSANDBOX_EPHEMERAL="+ephemeral)
}
