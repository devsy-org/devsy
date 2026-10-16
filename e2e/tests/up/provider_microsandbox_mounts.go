package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	microsandboxSecretAttachCommand = "attach"
	microsandboxSecretDeleteCommand = "delete"
	microsandboxSecretSetCommand    = "set"
	microsandboxVscodeImage         = "mcr.microsoft.com/devcontainers/base:ubuntu"
	microsandboxVscodeUser          = "vscode"
)

var _ = ginkgo.Describe("microsandbox mount parity",
	ginkgo.Label("up-provider-microsandbox-mounts"), func() {
		for _, provider := range microsandboxProviders("builtin", "external") {
			ginkgo.Context(provider.name, ginkgo.Label(provider.label), func() {
				var f *framework.Framework
				var workspace, volume string

				ginkgo.BeforeEach(func(ctx context.Context) {
					session := newMicrosandboxTestSession(ctx)
					f = session.framework
					workspace = session.newWorkspace()
					volume = ""
					session.addProvider(ctx, provider.source, provider.name)
					// Cleanup is LIFO: tear down the VM before attempting volume removal.
					ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
						if volume != "" {
							microsandboxMountCommand(cleanupCtx, "volume", "rm", volume)
						}
					})
					session.cleanupWorkspace(workspace)
				})

				ginkgo.It(
					"preserves bind policies and private secret tmpfs mounts",
					func(ctx context.Context) {
						framework.UseFileSecretsBackend()
						const (
							terminalFixtureName  = "MSB_TERMINAL_SECRET"
							terminalFixtureValue = "terminal-secret-fixture"
							fileFixtureName      = "MSB_FILE_SECRET"
							fileFixtureValue     = "file-secret-fixture"
						)
						storeMicrosandboxSecret(ctx, f, terminalFixtureName, terminalFixtureValue)
						storeMicrosandboxSecret(ctx, f, fileFixtureName, fileFixtureValue)
						_, err := f.ExecCommandOutput(
							ctx,
							[]string{
								secretCmd,
								microsandboxSecretAttachCommand,
								terminalFixtureName,
							},
						)
						framework.ExpectNoError(err)
						ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
							cleanupCtx, cancel := context.WithTimeout(
								context.WithoutCancel(cleanupCtx), time.Minute,
							)
							defer cancel()
							_, err := f.ExecCommandOutput(
								cleanupCtx,
								[]string{secretCmd, "detach", terminalFixtureName},
							)
							return err
						})

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
						writeMicrosandboxUserMountConfig(workspace, []string{
							"type=bind,source=" + writable + ",target=/parity-write,consistency=cached",
							"type=bind,source=" + readonly + ",target=/parity-read,readonly,consistency=delegated",
						}, microsandboxVscodeImage, microsandboxVscodeUser)
						framework.ExpectNoError(
							f.DevsyUp(
								ctx,
								workspace,
								"--devcontainer",
								".devcontainer.json",
								"--workspace-mount-consistency",
								"consistent",
								"--secret",
								fileFixtureName+",type=mount,target=microsandbox-secret",
							),
						)
						out, err := microsandboxSSHAsRootOnce(
							ctx,
							f,
							workspace,
							"cat /parity-write/from-host /parity-read/from-host && "+
								"printf 'guest\\n' > /parity-write/from-guest",
						)
						framework.ExpectNoError(err)
						gomega.Expect(out).To(gomega.Equal("host\nhost\n"))
						assertMicrosandboxSecretMounts(ctx, f, workspace)
						data, err := os.ReadFile(
							filepath.Join(writable, "from-guest"),
						) // #nosec G304 -- test-owned bind directory
						framework.ExpectNoError(err)
						gomega.Expect(string(data)).To(gomega.Equal("guest\n"))
						_, err = microsandboxSSHAsRootOnce(
							ctx,
							f,
							workspace,
							"printf 'forbidden\\n' > /parity-read/from-host",
						)
						gomega.Expect(err).To(gomega.HaveOccurred())
						// A subsequent successful read distinguishes mount enforcement from lost SSH connectivity.
						out, err = microsandboxSSHAsRootOnce(
							ctx, f, workspace, "cat /parity-read/from-host",
						)
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
						virtualized := policy != "off"
						if !virtualized {
							expectedOwner = owner
						}
						out, err := f.DevsySSHOnce(ctx, workspace, "stat -c '%u:%g %a' "+guestFile)
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal(expectedOwner + " 644"))
						if !virtualized {
							framework.ExpectNoError(os.Chmod(hostFile, 0o600))
						} else {
							_, err = f.DevsySSHOnce(ctx, workspace, "chmod 600 "+guestFile)
							framework.ExpectNoError(err)
							info, err := os.Stat(hostFile)
							framework.ExpectNoError(err)
							gomega.Expect(info.Mode().Perm()).To(gomega.Equal(os.FileMode(0o644)))
						}
						gomega.Expect(microsandboxHostOwner(ctx, hostFile)).To(gomega.Equal(owner))
						if !virtualized {
							// MicroSandbox v0.7.7 caches guest attributes for five seconds after stat.
							pollCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
							defer cancel()
							gomega.Eventually(pollCtx, func() string {
								out, err := f.DevsySSHOnce(
									pollCtx,
									workspace,
									"stat -c '%u:%g %a' "+guestFile,
								)
								if err != nil {
									gomega.StopTrying("read guest file attributes").Wrap(err).Now()
								}
								return strings.TrimSpace(out)
							}).WithTimeout(15 * time.Second).WithPolling(time.Second).
								Should(gomega.Equal(owner + " 600"))
							return
						}
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
	writeMicrosandboxUserMountConfig(dir, mounts, microsandboxResourceImage, microsandboxRootUser)
}

func writeMicrosandboxUserMountConfig(dir string, mounts []string, image, remoteUser string) {
	config := struct {
		Image         string   `json:"image"`
		ContainerUser string   `json:"containerUser"`
		RemoteUser    string   `json:"remoteUser"`
		Mounts        []string `json:"mounts,omitempty"`
	}{
		Image:         image,
		ContainerUser: microsandboxRootUser, RemoteUser: remoteUser,
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

func storeMicrosandboxSecret(
	ctx context.Context,
	f *framework.Framework,
	name, value string,
) {
	_, err := f.ExecCommandOutput(
		ctx,
		[]string{secretCmd, microsandboxSecretSetCommand, name, names.Flag(names.Value), value},
	)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(cleanupCtx), time.Minute,
		)
		defer cancel()
		_, err := f.ExecCommandOutput(
			cleanupCtx,
			[]string{secretCmd, microsandboxSecretDeleteCommand, name},
		)
		return err
	})
}

func assertMicrosandboxSecretMounts(ctx context.Context, f *framework.Framework, workspace string) {
	identity, err := f.DevsySSHOnce(
		ctx,
		workspace,
		"test \"$(id -un)\" = "+microsandboxVscodeUser+" && "+
			"test \"$(id -u)\" -ne 0 && printf 'nonroot-owner\\n'",
	)
	framework.ExpectNoError(err)
	gomega.Expect(identity).To(gomega.Equal("nonroot-owner\n"))

	out, err := f.DevsySSHOnce(ctx, workspace, `
		test "$(stat -f -c %T /run/devsy/secrets-env)" = tmpfs &&
		test "$(stat -c '%a:%U' /run/devsy/secrets-env)" = "700:$(id -un)" &&
		test "$(stat -c '%a:%U' /run/devsy/secrets-env/MSB_TERMINAL_SECRET)" = "600:$(id -un)" &&
		test "$(cat /run/devsy/secrets-env/MSB_TERMINAL_SECRET)" = 'terminal-secret-fixture' &&
		test "$MSB_TERMINAL_SECRET" = 'terminal-secret-fixture' &&
		test "$(stat -f -c %T /run/secrets)" = tmpfs &&
		test "$(stat -c %a /run/secrets)" = 755 &&
		test "$(stat -c '%a:%U' /run/secrets/microsandbox-secret)" = "600:$(id -un)" &&
		test "$(cat /run/secrets/microsandbox-secret)" = 'file-secret-fixture' &&
		printf 'secrets-ok\n'
	`)
	framework.ExpectNoError(err)
	gomega.Expect(out).To(gomega.Equal("secrets-ok\n"))
}

func microsandboxSSHAsRootOnce(
	ctx context.Context,
	f *framework.Framework,
	workspace, command string,
) (string, error) {
	stdout, stderr, err := f.ExecCommandCapture(ctx, []string{
		cmdWorkspace,
		cmdSSH,
		workspace,
		names.Flag(names.User),
		microsandboxRootUser,
		flagCommand,
		command,
		names.Flag(names.Debug),
	})
	if err != nil {
		return "", fmt.Errorf("devsy ssh as root failed: %w (stderr: %s)", err, stderr)
	}
	return stdout, nil
}

func microsandboxMountCommand(ctx context.Context, args ...string) {
	// #nosec G204 -- fixed runtime executable and test-owned volume arguments.
	out, err := exec.CommandContext(ctx, "msb", args...).CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("msb %v: %s", args, out))
}
