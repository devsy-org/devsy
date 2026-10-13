//go:build linux

package up

import (
	"context"
	"os"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const safeRecreateManagedConflictErr = "max_memory 2048 MiB must be greater than or equal to memory 3072 MiB"

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
				fixture.writeConfig("2gb")
				framework.ExpectNoError(setSafeRecreateManagedMemory3072())
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
					"request must reach final managed-layer memory validation",
				)
				// Inspect status and host state before any command can start a stopped VM.
				gomega.Expect(inspectSafeRecreate(ctx, fixture.sandbox)).
					To(gomega.Equal(fixture.original))
				fixture.assertHostState(ctx)

				// Restore policy before resuming stopped originals.
				framework.ExpectNoError(clearSafeRecreateManagedPolicy())
				fixture.inspectOriginalFiles(ctx, stopped)

				// The exact same request is valid without managed policy and must replace.
				fixture.writeConfig("2gb")
				framework.ExpectNoError(fixture.framework.DevsyUpRecreate(
					ctx, append([]string{fixture.workspace}, fixture.upArgs()...)...,
				))
				fixture.assertReplacement(ctx, 2048)
			},
			ginkgo.Entry(
				"builtin running",
				microsandboxResourceBuiltin,
				"msb-managed-builtin-running",
				false,
				ginkgo.Label("microsandbox-builtin"), ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"builtin stopped",
				microsandboxResourceBuiltin,
				"msb-managed-builtin-stopped",
				true,
				ginkgo.Label("microsandbox-builtin"), ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"external running",
				microsandboxExternalProvider,
				"msb-managed-external-running",
				false,
				ginkgo.Label("microsandbox-external"), ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"external stopped",
				microsandboxExternalProvider,
				"msb-managed-external-stopped",
				true,
				ginkgo.Label("microsandbox-external"), ginkgo.SpecTimeout(framework.TimeoutLong()),
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
