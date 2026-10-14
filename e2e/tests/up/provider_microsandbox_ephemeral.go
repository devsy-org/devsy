package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type microsandboxEphemeralCase struct {
	memoryMiB      uint64
	storageGiB     uint64
	preserveMounts bool
}

var _ = ginkgo.Describe("microsandbox ephemeral root parity",
	ginkgo.Label("up-provider-microsandbox-ephemeral"), func() {
		for _, provider := range microsandboxProviders("builtin-ephemeral", "external-ephemeral") {
			ginkgo.Context(provider.name, ginkgo.Label(provider.label), func() {
				var f *framework.Framework
				var workspace, volume, ownedLabel string
				var volumeOwned bool

				ginkgo.BeforeEach(func(ctx context.Context) {
					skipIfNoMicrosandbox(ctx)
					ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
					ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
					initialDir, err := os.Getwd()
					framework.ExpectNoError(err)
					f = framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
					workspace, err = framework.CreateTempDir()
					framework.ExpectNoError(err)
					volume, volumeOwned, ownedLabel = "", false, ""
					ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, workspace)
					framework.ExpectNoError(
						f.DevsyProviderAdd(ctx, provider.source, "--name", provider.name),
					)
					ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
						cleanupCtx, cancel := context.WithTimeout(
							context.WithoutCancel(cleanupCtx),
							time.Minute,
						)
						defer cancel()
						return f.DevsyProviderDelete(cleanupCtx, provider.name)
					})
					// A rejected creation can lose its Devsy record before runtime cleanup completes.
					ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
						if ownedLabel == "" {
							return nil
						}
						cleanupCtx, cancel := context.WithTimeout(
							context.WithoutCancel(cleanupCtx),
							time.Minute,
						)
						defer cancel()
						out, err := microsandboxResourceCommand(
							cleanupCtx,
							"remove",
							"--force",
							"--label",
							ownedLabel,
						)
						if err != nil {
							return fmt.Errorf(
								"remove test-owned sandbox with label %s: %w: %s",
								ownedLabel,
								err,
								out,
							)
						}
						return nil
					})
					// Independent LIFO cleanup attempts stop the VM before removing our volume.
					ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
						if !volumeOwned {
							return nil
						}
						cleanupCtx, cancel := context.WithTimeout(
							context.WithoutCancel(cleanupCtx),
							time.Minute,
						)
						defer cancel()
						out, err := microsandboxResourceCommand(cleanupCtx, "volume", "rm", volume)
						if err != nil {
							return fmt.Errorf(
								"remove test-owned volume %s: %w: %s",
								volume,
								err,
								out,
							)
						}
						return nil
					})
					ginkgo.DeferCleanup(f.CleanupWorkspace, workspace)
					framework.ExpectNoError(
						os.WriteFile(filepath.Join(workspace, microsandboxStorageMarker),
							[]byte("host-preserved\n"), 0o600),
					)
				})

				ginkgo.DescribeTable(
					"resets the tmpfs root without discarding the sandbox record",
					func(ctx context.Context, testCase microsandboxEphemeralCase) {
						options := []string{
							microsandboxResourceOption,
							"MICROSANDBOX_EPHEMERAL=true",
							microsandboxResourceOption,
							fmt.Sprintf("MICROSANDBOX_MEMORY=%d", testCase.memoryMiB),
						}
						if testCase.storageGiB != 0 {
							options = append(options, microsandboxResourceOption,
								fmt.Sprintf("MICROSANDBOX_STORAGE=%d", testCase.storageGiB))
						}
						framework.ExpectNoError(f.DevsyProviderUse(ctx, provider.name, options...))
						var mounts []string
						if testCase.preserveMounts {
							volume = "devsy-ephemeral-parity-" + filepath.Base(workspace)
							out, err := microsandboxResourceCommand(ctx, "volume", "create", volume)
							framework.ExpectNoError(err, string(out))
							volumeOwned = true
							mounts = []string{
								"type=volume,source=" + volume + ",target=/ephemeral-volume",
							}
						}
						writeMicrosandboxMountConfig(workspace, mounts)
						framework.ExpectNoError(
							f.DevsyUp(ctx, workspace, "--devcontainer", ".devcontainer.json"),
						)
						sandbox := microsandboxResourceSandbox(ctx, f, workspace)
						assertMicrosandboxEphemeralRoot(ctx, f, workspace)
						assertMicrosandboxStorageWorkspace(ctx, f, workspace)
						createdAt := microsandboxCreationTime(ctx, sandbox)
						bootID := microsandboxStorageBootID(ctx, f, workspace)
						writeMicrosandboxEphemeralRootMarker(ctx, f, workspace)
						if testCase.preserveMounts {
							out, err := f.DevsySSHOnce(ctx, workspace,
								"printf 'volume-preserved\\n' > /ephemeral-volume/marker")
							framework.ExpectNoError(err, out)
							assertMicrosandboxEphemeralVolume(ctx, f, workspace)
						}

						framework.ExpectNoError(f.DevsyWorkspaceStop(ctx, workspace))
						assertMicrosandboxEphemeralStopped(ctx, sandbox, createdAt)
						framework.ExpectNoError(f.DevsyUp(ctx, workspace))
						gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
							To(gomega.Equal(createdAt))
						restartedBootID := microsandboxStorageBootID(ctx, f, workspace)
						gomega.Expect(restartedBootID).NotTo(gomega.Equal(bootID))
						assertMicrosandboxEphemeralReset(ctx, f, workspace)
						if !testCase.preserveMounts {
							return
						}
						assertMicrosandboxEphemeralVolume(ctx, f, workspace)
						writeMicrosandboxEphemeralRootMarker(ctx, f, workspace)
						framework.ExpectNoError(f.DevsyUpRecreate(ctx, workspace))
						gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
							NotTo(gomega.Equal(createdAt))
						gomega.Expect(microsandboxStorageBootID(ctx, f, workspace)).
							NotTo(gomega.Equal(restartedBootID))
						assertMicrosandboxEphemeralReset(ctx, f, workspace)
						assertMicrosandboxEphemeralVolume(ctx, f, workspace)
					},
					ginkgo.Entry(
						"defaults to half of 2048 MiB and preserves bind and volume data through recreation",
						microsandboxEphemeralCase{memoryMiB: 2048, preserveMounts: true},
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry("explicit 1 GiB storage overrides half of 3072 MiB",
						microsandboxEphemeralCase{memoryMiB: 3072, storageGiB: 1},
						ginkgo.SpecTimeout(framework.TimeoutLong())),
				)

				ginkgo.It(
					"rejects a fresh tmpfs root larger than memory without creating a VM or deleting source",
					func(ctx context.Context) {
						framework.ExpectNoError(f.DevsyProviderUse(ctx, provider.name,
							microsandboxResourceOption, "MICROSANDBOX_EPHEMERAL=true",
							microsandboxResourceOption, "MICROSANDBOX_MEMORY=1024",
							microsandboxResourceOption, "MICROSANDBOX_STORAGE=2"))
						writeMicrosandboxMountConfig(workspace, nil)
						configPath := filepath.Join(workspace, ".devcontainer.json")
						configBefore, err := os.ReadFile(
							configPath,
						) // #nosec G304 -- test-owned configuration
						framework.ExpectNoError(err)
						label := "devsy.ephemeral-parity=" + filepath.Base(workspace)
						assertMicrosandboxEphemeralAbsent(ctx, label)
						ownedLabel = label
						stdout, stderr, err := f.DevsyUpStreams(ctx, workspace,
							"--devcontainer", ".devcontainer.json", "--id-label", label)
						gomega.Expect(err).To(gomega.HaveOccurred())
						gomega.Expect(stdout + stderr).To(gomega.ContainSubstring(
							"tmpfs root disk size (2048 MiB) must not exceed sandbox memory (1024 MiB)",
						))
						assertMicrosandboxEphemeralAbsent(ctx, label)
						configAfter, err := os.ReadFile(
							configPath,
						) // #nosec G304 -- test-owned configuration
						framework.ExpectNoError(err)
						gomega.Expect(configAfter).To(gomega.Equal(configBefore))
						marker, err := os.ReadFile(
							filepath.Join(workspace, microsandboxStorageMarker),
						) // #nosec G304 -- test-owned source
						framework.ExpectNoError(err)
						gomega.Expect(string(marker)).To(gomega.Equal("host-preserved\n"))
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)
			})
		}
	})

func assertMicrosandboxEphemeralRoot(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) {
	sandbox := microsandboxResourceSandbox(ctx, f, workspace)
	out, err := microsandboxResourceCommand(
		ctx,
		"inspect",
		sandbox,
		"--format",
		microsandboxResourceJSON,
	)
	framework.ExpectNoError(err, string(out))
	var inspection struct {
		ActiveConfig *microsandboxStorageConfig `json:"active_config"`
	}
	framework.ExpectNoError(json.Unmarshal(out, &inspection), string(out))
	gomega.Expect(inspection.ActiveConfig).NotTo(gomega.BeNil())
	gomega.Expect(inspection.ActiveConfig.Image.OCI).NotTo(gomega.BeNil())
	rootDisk := inspection.ActiveConfig.Image.OCI.RootDisk
	gomega.Expect(rootDisk).NotTo(gomega.BeNil())
	gomega.Expect(rootDisk.Kind).To(gomega.Equal("tmpfs"))
	gomega.Expect(rootDisk.SizeMiB).To(gomega.Equal(uint64(1024)))

	// The guest root is overlayfs; measure its writable backing capacity, not its filesystem type.
	capacity, err := f.DevsySSHOnce(ctx, workspace, "stat -f -c '%S %b' /")
	framework.ExpectNoError(err)
	var blockSize, blocks uint64
	n, err := fmt.Sscanf(strings.TrimSpace(capacity), "%d %d", &blockSize, &blocks)
	framework.ExpectNoError(err)
	gomega.Expect(n).To(gomega.Equal(2))
	gomega.Expect(blockSize).To(gomega.BeNumerically(">", 0))
	totalBytes := blockSize * blocks
	const expectedBytes = uint64(1024 * 1024 * 1024)
	gomega.Expect(totalBytes).To(gomega.BeNumerically(">=", expectedBytes*9/10))
	gomega.Expect(totalBytes).To(gomega.BeNumerically("<=", expectedBytes))
}

func writeMicrosandboxEphemeralRootMarker(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) {
	out, err := f.DevsySSHOnce(
		ctx,
		workspace,
		"printf 'root-written\\n' > /root/ephemeral-parity-marker && cat /root/ephemeral-parity-marker",
	)
	framework.ExpectNoError(err)
	gomega.Expect(out).To(gomega.Equal("root-written\n"))
}

func assertMicrosandboxEphemeralReset(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) {
	out, err := f.DevsySSHOnce(ctx, workspace, "test ! -e /root/ephemeral-parity-marker")
	framework.ExpectNoError(err, out)
	assertMicrosandboxEphemeralRoot(ctx, f, workspace)
	assertMicrosandboxStorageWorkspace(ctx, f, workspace)
}

func assertMicrosandboxEphemeralVolume(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) {
	out, err := f.DevsySSHOnce(ctx, workspace, "cat /ephemeral-volume/marker")
	framework.ExpectNoError(err)
	gomega.Expect(out).To(gomega.Equal("volume-preserved\n"))
}

func assertMicrosandboxEphemeralStopped(ctx context.Context, sandbox, createdAt string) {
	out, err := microsandboxResourceCommand(
		ctx,
		"inspect",
		sandbox,
		"--format",
		microsandboxResourceJSON,
	)
	framework.ExpectNoError(err, string(out))
	var inspection struct {
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	framework.ExpectNoError(json.Unmarshal(out, &inspection), string(out))
	gomega.Expect(inspection.Status).To(gomega.Equal("Stopped"))
	gomega.Expect(inspection.CreatedAt).To(gomega.Equal(createdAt))
}

func assertMicrosandboxEphemeralAbsent(ctx context.Context, label string) {
	out, err := microsandboxResourceCommand(
		ctx,
		"list",
		"--label",
		label,
		"--format",
		microsandboxResourceJSON,
	)
	framework.ExpectNoError(err, string(out))
	var sandboxes []json.RawMessage
	framework.ExpectNoError(json.Unmarshal(out, &sandboxes), string(out))
	gomega.Expect(sandboxes).
		To(gomega.BeEmpty(), "the failed fresh creation must not leave a test-labeled sandbox")
}
