package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const microsandboxStorageMarker = "storage-host-marker"

type microsandboxStorageConfig struct {
	Image microsandboxStorageImage `json:"image"`
}

type microsandboxStorageImage struct {
	OCI *microsandboxStorageOCI `json:"Oci"`
}

type microsandboxStorageOCI struct {
	RootDisk *microsandboxStorageDisk `json:"root_disk"`
}

type microsandboxStorageDisk struct {
	Kind    string `json:"kind"`
	SizeMiB uint64 `json:"size_mib"`
}

var _ = ginkgo.Describe("microsandbox storage parity",
	ginkgo.Label("up-provider-microsandbox-storage"), func() {
		for _, provider := range microsandboxProviders("builtin-storage", "external-storage") {
			ginkgo.Context(provider.name, ginkgo.Label(provider.label), func() {
				var f *framework.Framework
				var workspace string

				ginkgo.BeforeEach(func(ctx context.Context) {
					session := newMicrosandboxTestSession(ctx)
					f = session.framework
					workspace = session.newWorkspace()
					session.addProvider(ctx, provider.source, provider.name)
					session.cleanupWorkspace(workspace)
				})

				ginkgo.DescribeTable(
					"sizes and preserves the managed root until recreation",
					func(ctx context.Context, options []string, storage string, sizeMiB uint64) {
						framework.ExpectNoError(f.DevsyProviderUse(ctx, provider.name, options...))
						var requirements *config.HostRequirements
						if storage != "" {
							requirements = &config.HostRequirements{Storage: storage}
						}
						writeMicrosandboxResourceConfig(workspace, requirements)
						framework.ExpectNoError(os.WriteFile(
							filepath.Join(
								workspace,
								microsandboxStorageMarker,
							),
							[]byte("host-preserved\n"),
							0o600,
						))
						framework.ExpectNoError(
							f.DevsyUp(ctx, workspace, "--devcontainer", ".devcontainer.json"),
						)
						sandbox := microsandboxResourceSandbox(ctx, f, workspace)
						assertMicrosandboxStorage(ctx, f, workspace, sizeMiB)
						assertMicrosandboxStorageWorkspace(ctx, f, workspace)
						createdAt := microsandboxCreationTime(ctx, sandbox)
						bootID := microsandboxStorageBootID(ctx, f, workspace)
						out, err := f.DevsySSHOnce(ctx, workspace,
							"printf 'root-preserved\\n' > /root/storage-parity-marker")
						framework.ExpectNoError(err, out)

						framework.ExpectNoError(f.DevsyWorkspaceStop(ctx, workspace))
						framework.ExpectNoError(f.DevsyUp(ctx, workspace))
						gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
							To(gomega.Equal(createdAt))
						restartedBootID := microsandboxStorageBootID(ctx, f, workspace)
						gomega.Expect(restartedBootID).NotTo(gomega.Equal(bootID))
						out, err = f.DevsySSHOnce(ctx, workspace, "cat /root/storage-parity-marker")
						framework.ExpectNoError(err)
						gomega.Expect(out).To(gomega.Equal("root-preserved\n"))
						assertMicrosandboxStorage(ctx, f, workspace, sizeMiB)
						assertMicrosandboxStorageWorkspace(ctx, f, workspace)

						framework.ExpectNoError(f.DevsyUpRecreate(ctx, workspace))
						gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
							NotTo(gomega.Equal(createdAt))
						gomega.Expect(microsandboxStorageBootID(ctx, f, workspace)).
							NotTo(gomega.Equal(restartedBootID))
						out, err = f.DevsySSHOnce(
							ctx,
							workspace,
							"test ! -e /root/storage-parity-marker",
						)
						framework.ExpectNoError(err, out)
						assertMicrosandboxStorage(ctx, f, workspace, sizeMiB)
						assertMicrosandboxStorageWorkspace(ctx, f, workspace)
					},
					ginkgo.Entry(
						"runtime default",
						nil,
						"",
						uint64(4096),
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry("provider overrides host requirements",
						[]string{"--option", "MICROSANDBOX_STORAGE=2"}, "3gb", uint64(2048),
						ginkgo.SpecTimeout(framework.TimeoutLong())),
					ginkgo.Entry("zero provider storage rounds host requirements up to GiB",
						[]string{"--option", "MICROSANDBOX_STORAGE=0"}, "1537mb", uint64(2048),
						ginkgo.SpecTimeout(framework.TimeoutLong())),
				)
			})
		}
	})

func assertMicrosandboxStorage(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
	sizeMiB uint64,
) {
	sandbox := microsandboxResourceSandbox(ctx, f, workspace)
	out, err := microsandboxResourceCommand(ctx, "inspect", sandbox, "--format", "json")
	framework.ExpectNoError(err, string(out))
	var inspection struct {
		ActiveConfig *microsandboxStorageConfig `json:"active_config"`
	}
	framework.ExpectNoError(json.Unmarshal(out, &inspection), string(out))
	gomega.Expect(inspection.ActiveConfig).NotTo(gomega.BeNil())
	gomega.Expect(inspection.ActiveConfig.Image.OCI).NotTo(gomega.BeNil())
	rootDisk := inspection.ActiveConfig.Image.OCI.RootDisk
	gomega.Expect(rootDisk).NotTo(gomega.BeNil())
	gomega.Expect(rootDisk.Kind).To(gomega.Equal("managed"))
	gomega.Expect(rootDisk.SizeMiB).To(gomega.Equal(sizeMiB))

	capacity, err := f.DevsySSHOnce(ctx, workspace, "stat -f -c '%S %b' /")
	framework.ExpectNoError(err)
	var blockSize, blocks uint64
	n, err := fmt.Sscanf(strings.TrimSpace(capacity), "%d %d", &blockSize, &blocks)
	framework.ExpectNoError(err)
	gomega.Expect(n).To(gomega.Equal(2))
	gomega.Expect(blockSize).To(gomega.BeNumerically(">", 0))
	totalBytes := blockSize * blocks
	expectedBytes := sizeMiB * 1024 * 1024
	// Ext4 metadata reduces statfs total blocks; the 2 GiB and 4 GiB ranges remain disjoint.
	gomega.Expect(totalBytes).To(gomega.BeNumerically(">=", expectedBytes*9/10))
	gomega.Expect(totalBytes).To(gomega.BeNumerically("<=", expectedBytes))
}

func assertMicrosandboxStorageWorkspace(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) {
	data, err := os.ReadFile(
		filepath.Join(workspace, microsandboxStorageMarker),
	) // #nosec G304 -- test-owned workspace
	framework.ExpectNoError(err)
	gomega.Expect(string(data)).To(gomega.Equal("host-preserved\n"))
	guestMarker := filepath.Join("/workspaces", filepath.Base(workspace), microsandboxStorageMarker)
	out, err := f.DevsySSHOnce(ctx, workspace, "cat "+guestMarker)
	framework.ExpectNoError(err)
	gomega.Expect(out).To(gomega.Equal("host-preserved\n"))
}

func microsandboxStorageBootID(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) string {
	out, err := f.DevsySSHOnce(ctx, workspace, "cat /proc/sys/kernel/random/boot_id")
	framework.ExpectNoError(err)
	bootID := strings.TrimSpace(out)
	gomega.Expect(bootID).To(gomega.MatchRegexp(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`))
	return bootID
}
