package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("microsandbox mount parity",
	ginkgo.Label("up-provider-microsandbox-mounts"), func() {
		for _, provider := range []struct{ name, source string }{
			{"builtin", "microsandbox"},
			{"external", microsandboxExternalProvider},
		} {
			ginkgo.Context(provider.name, func() {
				var f *framework.Framework
				var workspace, volume string

				ginkgo.BeforeEach(func(ctx context.Context) {
					skipIfNoMicrosandbox(ctx)
					ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
					ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
					initialDir, err := os.Getwd()
					framework.ExpectNoError(err)
					f = framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
					workspace, err = framework.CreateTempDir()
					framework.ExpectNoError(err)
					volume = ""
					ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, workspace)
					framework.ExpectNoError(
						f.DevsyProviderAdd(ctx, provider.source, "--name", provider.name),
					)
					ginkgo.DeferCleanup(f.DevsyProviderDelete, provider.name)
					ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
						framework.ExpectNoError(f.CleanupWorkspace(cleanupCtx, workspace))
						if volume != "" {
							microsandboxMountCommand(cleanupCtx, "volume", "rm", volume)
						}
					})
				})

				ginkgo.It(
					"shares writable binds and rejects writes to read-only binds",
					func(ctx context.Context) {
						writable, readonly := ginkgo.GinkgoT().TempDir(), ginkgo.GinkgoT().TempDir()
						for _, dir := range []string{writable, readonly} {
							framework.ExpectNoError(
								os.WriteFile(
									filepath.Join(dir, "from-host"),
									[]byte("host\n"),
									0o600,
								),
							)
						}
						writeMicrosandboxMountConfig(workspace, []string{
							"type=bind,source=" + writable + ",target=/parity-write",
							"type=bind,source=" + readonly + ",target=/parity-read,readonly",
						})
						framework.ExpectNoError(
							f.DevsyUp(ctx, workspace, "--devcontainer", ".devcontainer.json"),
						)
						out, err := f.DevsySSHOnce(
							ctx,
							workspace,
							"cat /parity-write/from-host /parity-read/from-host && printf 'guest\\n' > /parity-write/from-guest",
						)
						framework.ExpectNoError(err)
						gomega.Expect(out).To(gomega.Equal("host\nhost\n"))
						data, err := os.ReadFile(
							filepath.Join(writable, "from-guest"),
						) // #nosec G304 -- test-owned bind directory
						framework.ExpectNoError(err)
						gomega.Expect(string(data)).To(gomega.Equal("guest\n"))
						_, err = f.DevsySSHOnce(
							ctx,
							workspace,
							"printf 'forbidden\\n' > /parity-read/from-host",
						)
						gomega.Expect(err).To(gomega.HaveOccurred())
						// A subsequent successful read distinguishes mount enforcement from lost SSH connectivity.
						out, err = f.DevsySSHOnce(ctx, workspace, "cat /parity-read/from-host")
						framework.ExpectNoError(err)
						gomega.Expect(out).To(gomega.Equal("host\n"))
						data, err = os.ReadFile(
							filepath.Join(readonly, "from-host"),
						) // #nosec G304 -- test-owned bind directory
						framework.ExpectNoError(err)
						gomega.Expect(string(data)).To(gomega.Equal("host\n"))
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)

				ginkgo.It(
					"preserves named-volume data and resets tmpfs across restart and recreation",
					func(ctx context.Context) {
						volume = "devsy-mount-parity-" + filepath.Base(workspace)
						microsandboxMountCommand(ctx, "volume", "create", volume)
						writeMicrosandboxMountConfig(workspace, []string{
							"type=volume,source=" + volume + ",target=/parity-volume",
							"type=tmpfs,target=/parity-scratch",
						})
						framework.ExpectNoError(
							f.DevsyUp(ctx, workspace, "--devcontainer", ".devcontainer.json"),
						)
						out, err := f.DevsySSHOnce(ctx, workspace,
							"test \"$(stat -f -c %T /parity-scratch)\" = tmpfs && "+
								"printf 'persistent\\n' > /parity-volume/marker && printf 'scratch\\n' > /parity-scratch/marker")
						framework.ExpectNoError(err, out)
						framework.ExpectNoError(f.DevsyWorkspaceStop(ctx, workspace))
						framework.ExpectNoError(f.DevsyUp(ctx, workspace))
						assertMicrosandboxPersistentMounts(ctx, f, workspace)
						_, err = f.DevsySSHOnce(
							ctx,
							workspace,
							"printf 'scratch-again\\n' > /parity-scratch/marker",
						)
						framework.ExpectNoError(err)
						framework.ExpectNoError(f.DevsyUpRecreate(ctx, workspace))
						assertMicrosandboxPersistentMounts(ctx, f, workspace)
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)

				ginkgo.DescribeTable(
					"honors workspace stat virtualization with private host permissions",
					func(ctx context.Context, policy string) {
						framework.ExpectNoError(f.DevsyProviderUse(ctx, provider.name,
							"--option", "MICROSANDBOX_WORKSPACE_HOST_PERMISSIONS=private",
							"--option", "MICROSANDBOX_WORKSPACE_STAT_VIRTUALIZATION="+policy))
						writeMicrosandboxMountConfig(workspace, nil)
						framework.ExpectNoError(
							f.DevsyUp(ctx, workspace, "--devcontainer", ".devcontainer.json"),
						)
						// Create after setup so workspace chown cannot supply the guest fallback identity.
						hostFile := filepath.Join(workspace, "policy-file")
						framework.ExpectNoError(os.WriteFile(hostFile, []byte("host\n"), 0o600))
						// CI runs as root; keep literal host ownership distinct from the guest fallback.
						if os.Geteuid() == 0 {
							framework.ExpectNoError(os.Chown(hostFile, 10001, 10002))
						}
						//nolint:gosec // explicit host modes are the behavior under test
						framework.ExpectNoError(os.Chmod(hostFile, 0o644))
						owner := microsandboxHostOwner(ctx, hostFile)
						gomega.Expect(owner).NotTo(gomega.Equal("0:0"))
						guestFile := "/workspaces/" + filepath.Base(workspace) + "/policy-file"
						expectedOwner := "0:0"
						if policy == "off" {
							expectedOwner = owner
						}
						out, err := f.DevsySSHOnce(ctx, workspace, "stat -c '%u:%g %a' "+guestFile)
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal(expectedOwner + " 644"))
						if policy == "off" {
							framework.ExpectNoError(os.Chmod(hostFile, 0o600))
						} else {
							_, err = f.DevsySSHOnce(ctx, workspace, "chmod 600 "+guestFile)
							framework.ExpectNoError(err)
							info, err := os.Stat(hostFile)
							framework.ExpectNoError(err)
							gomega.Expect(info.Mode().Perm()).To(gomega.Equal(os.FileMode(0o644)))
						}
						gomega.Expect(microsandboxHostOwner(ctx, hostFile)).To(gomega.Equal(owner))
						out, err = f.DevsySSHOnce(ctx, workspace, "stat -c '%u:%g %a' "+guestFile)
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal(expectedOwner + " 600"))
					},
					ginkgo.Entry("strict", "strict", ginkgo.SpecTimeout(framework.TimeoutLong())),
					ginkgo.Entry("relaxed", "relaxed", ginkgo.SpecTimeout(framework.TimeoutLong())),
					ginkgo.Entry(
						"off exposes host metadata",
						"off",
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
				)
			})
		}
	})

func writeMicrosandboxMountConfig(dir string, mounts []string) {
	config := struct {
		Image         string   `json:"image"`
		ContainerUser string   `json:"containerUser"`
		RemoteUser    string   `json:"remoteUser"`
		Mounts        []string `json:"mounts,omitempty"`
	}{
		Image:         "ghcr.io/devsy-org/test-images/base:alpine",
		ContainerUser: microsandboxRootUser, RemoteUser: microsandboxRootUser,
		Mounts: mounts,
	}
	data, err := json.Marshal(config)
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(filepath.Join(dir, ".devcontainer.json"), data, 0o600))
}

func assertMicrosandboxPersistentMounts(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) {
	out, err := f.DevsySSHOnce(ctx, workspace,
		"test \"$(stat -f -c %T /parity-scratch)\" = tmpfs && "+
			"test ! -e /parity-scratch/marker && cat /parity-volume/marker")
	framework.ExpectNoError(err)
	gomega.Expect(out).To(gomega.Equal("persistent\n"))
}

func microsandboxMountCommand(ctx context.Context, args ...string) {
	// #nosec G204 -- fixed runtime executable and test-owned volume arguments.
	out, err := exec.CommandContext(ctx, "msb", args...).CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("msb %v: %s", args, out))
}
