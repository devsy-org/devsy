package up

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for podman provider",
	ginkgo.Label("up-provider-podman"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.Context("with rootless podman", func() {
			var f *framework.Framework

			ginkgo.BeforeEach(func(ctx context.Context) {
				var err error
				f, err = setupDockerProvider(initialDir+"/bin", "podman")
				framework.ExpectNoError(err)
			})

			ginkgo.Context("basic", func() {
				ginkgo.It(
					"should start a new workspace with existing image",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("build", func() {
				ginkgo.It(
					"should start a workspace with a multistage Dockerfile build",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-with-multi-stage-build",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)

				ginkgo.It(
					"should build and respect overrideCommand false",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-override-command-false",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("lifecycle commands", func() {
				ginkgo.It(
					"should run postCreateCommand with object syntax",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-postcreate-parallel",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						one, err := f.DevsySSH(ctx, tempDir, "cat /tmp/post-create-one.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(one)).To(gomega.Equal("postCreateOne"))

						two, err := f.DevsySSH(ctx, tempDir, "cat /tmp/post-create-two.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(two)).To(gomega.Equal("postCreateTwo"))
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It("should run postStartCommand after restart", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-post-start-restart",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/post-start-count.log")
					framework.ExpectNoError(err)
					lines := strings.Count(strings.TrimSpace(out), "\n") + 1
					gomega.Expect(lines).To(gomega.Equal(1),
						"postStartCommand should have run once after initial up")

					err = f.DevsyWorkspaceStop(ctx, tempDir)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir)
					framework.ExpectNoError(err)

					out, err = f.DevsySSH(ctx, tempDir, "cat $HOME/post-start-count.log")
					framework.ExpectNoError(err)
					lines = strings.Count(strings.TrimSpace(out), "\n") + 1
					gomega.Expect(lines).To(gomega.Equal(2),
						"postStartCommand should have run again after restart")
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It(
					"should defer postCreateCommand to background with waitFor",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-waitfor",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/on-create.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("onCreateDone"))

						out, err = f.DevsySSH(ctx, tempDir, "cat $HOME/update-content.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("updateContentDone"))

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/deferred.marker 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postCreateDone"),
						)

						envPath, err := f.DevsySSH(
							ctx, tempDir, "cat $HOME/deferred-env-path.out",
						)
						framework.ExpectNoError(err)
						gomega.Expect(envPath).To(
							gomega.ContainSubstring("/usr/local/bin"),
						)
						gomega.Expect(envPath).NotTo(gomega.ContainSubstring("${containerEnv:"))

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx,
								tempDir,
								"cat $HOME/post-start-deferred.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postStartDone"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should make IDE accessible before postAttachCommand completes",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-post-attach-nonblocking",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/post-start.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("postStartDone"))

						_, err = f.DevsySSH(ctx, tempDir, "cat $HOME/post-attach.out")
						gomega.Expect(err).To(gomega.HaveOccurred())

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/post-attach.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postAttachDone"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should run postAttachCommand on every attach",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-post-attach-every-time",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/attach-count.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(15 * time.Second).WithPolling(1 * time.Second).Should(
							gomega.Equal("1"),
						)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/attach-count.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(15 * time.Second).WithPolling(1 * time.Second).Should(
							gomega.Equal("2"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should run initializeCommand with object syntax",
					func(ctx context.Context) {
						tempDir, err := setupWorkspaceAndUp(
							ctx,
							"tests/up/testdata/docker-initcmd-parallel",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						one, err := os.ReadFile( //nolint:gosec // G304
							filepath.Join(tempDir, "init-cmd-one.out"),
						)
						framework.ExpectNoError(err)
						gomega.Expect(string(one)).To(gomega.Equal("initCmdOne"))

						two, err := os.ReadFile( //nolint:gosec // G304
							filepath.Join(tempDir, "init-cmd-two.out"),
						)
						framework.ExpectNoError(err)
						gomega.Expect(string(two)).To(gomega.Equal("initCmdTwo"))
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should inject secrets-file env into lifecycle commands",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-secrets-file",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						secretsDir, err := framework.CreateTempDir()
						framework.ExpectNoError(err)
						ginkgo.DeferCleanup(func() { _ = os.RemoveAll(secretsDir) })

						secretsFile := filepath.Join(secretsDir, "secrets.env")
						err = os.WriteFile(
							secretsFile,
							[]byte("MY_SECRET=test-value-12345\nANOTHER_SECRET=second-secret-42\n"),
							0o600,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir, "--secrets-file", secretsFile)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat /tmp/secret-check.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal("test-value-12345"))

						out, err = f.DevsySSH(
							ctx, tempDir, "cat /tmp/another-secret-check.out",
						)
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal("second-secret-42"))
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("agent delivery", func() {
				ginkgo.It(
					"should deliver the agent binary and execute SSH commands",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						err = f.DevsySSHEchoTestString(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("exec", func() {
				ginkgo.It(
					"should execute commands inside the container via SSH",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "echo -n hello-podman")
						framework.ExpectNoError(err)
						framework.ExpectEqual(out, "hello-podman")

						out, err = f.DevsySSH(ctx, tempDir, "pwd")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).NotTo(gomega.BeEmpty())
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("cleanup", func() {
				ginkgo.It(
					"should delete workspace and clean up resources",
					func(ctx context.Context) {
						tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker")
						framework.ExpectNoError(err)
						ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						_, err = f.FindWorkspace(ctx, tempDir)
						framework.ExpectNoError(err)

						err = f.DevsyWorkspaceDelete(ctx, tempDir)
						framework.ExpectNoError(err)

						_, err = f.FindWorkspace(ctx, tempDir)
						framework.ExpectError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("configuration", func() { //nolint:dupl
				ginkgo.It("should substitute variables", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-variables",
						initialDir,
						f,
						"--init-env", "CUSTOM_VAR=custom_value",
						"--init-env", "CUSTOM_IMAGE=ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					devContainerID, err := f.DevsySSH(
						ctx,
						tempDir,
						"cat $HOME/dev-container-id.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(devContainerID)).NotTo(gomega.BeEmpty())

					containerEnvPath, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-env-path.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(containerEnvPath).To(gomega.ContainSubstring("/usr/local/bin"))

					localEnvHome, err := f.DevsySSH(ctx, tempDir, "cat $HOME/local-env-home.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(localEnvHome)).
						To(gomega.Equal(os.Getenv("HOME")))

					localWorkspaceFolder, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/local-workspace-folder.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(
						framework.CleanString(strings.TrimSpace(localWorkspaceFolder)),
					).To(gomega.Equal(framework.CleanString(tempDir)))

					localWorkspaceFolderBasename, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/local-workspace-folder-basename.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(localWorkspaceFolderBasename)).
						To(gomega.Equal(filepath.Base(tempDir)))

					containerWorkspaceFolder, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-workspace-folder.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(
						framework.CleanString(strings.TrimSpace(containerWorkspaceFolder)),
					).To(gomega.Equal(
						framework.CleanString(path.Join("/workspaces", filepath.Base(tempDir))),
					))

					containerWorkspaceFolderBasename, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-workspace-folder-basename.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(containerWorkspaceFolderBasename)).
						To(gomega.Equal(filepath.Base(tempDir)))

					customVar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/custom-var.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(customVar)).To(gomega.Equal("custom_value"))

					customImage, err := f.DevsySSH(ctx, tempDir, "cat $HOME/custom-image.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(customImage)).
						To(gomega.Equal("ghcr.io/devsy-org/test-images/base:alpine"))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should substitute variables with defaults", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-variables-defaults",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					withDefault, err := f.DevsySSH(ctx, tempDir, "cat $HOME/with-default.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(withDefault)).
						To(gomega.Equal("my_default_value"))

					colonDefault, err := f.DevsySSH(ctx, tempDir, "cat $HOME/colon-default.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(colonDefault)).
						To(gomega.Equal("http://proxy:8080"))

					setVar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/set-var.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(setVar)).To(gomega.Equal(os.Getenv("HOME")))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should unset variable with remoteEnv null", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-remote-env-null",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					setLine, err := f.DevsySSH(ctx, tempDir, "head -1 $HOME/remote-env-null.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(setLine)).To(gomega.Equal("SET=hello"))

					unsetLine, err := f.DevsySSH(
						ctx, tempDir, "tail -1 $HOME/remote-env-null.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(unsetLine)).To(gomega.Equal("UNSET=true"))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should merge extra devcontainer config", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-extra-devcontainer",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					extraPath := path.Join(tempDir, "extra.json")
					err = f.DevsyUp(ctx, tempDir, "--extra-devcontainer-path", extraPath)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "bash -l -c 'echo -n $BASE_VAR'")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "base_value")

					out, err = f.DevsySSH(ctx, tempDir, "bash -l -c 'echo -n $EXTRA_VAR'")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "extra_value")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It(
					"should override with extra devcontainer config",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-extra-override",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						extraPath := path.Join(tempDir, "override.json")
						err = f.DevsyUp(ctx, tempDir, "--extra-devcontainer-path", extraPath)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat /tmp/test-var.out")
						framework.ExpectNoError(err)
						framework.ExpectEqual(strings.TrimSpace(out), "overridden_value")

						err = f.DevsyWorkspaceDelete(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It("should select from multiple devcontainers", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-multi-devcontainer",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir, "--devcontainer-id", "python")
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(
						ctx, tempDir, "bash -l -c 'echo -n $DEVCONTAINER_TYPE'",
					)
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "python")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir, "--devcontainer-id", "go")
					framework.ExpectNoError(err)

					out, err = f.DevsySSH(
						ctx, tempDir, "bash -l -c 'echo -n $DEVCONTAINER_TYPE'",
					)
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "go")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))
			})

			ginkgo.Context("features", func() {
				ginkgo.It("should mount volumes", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-mounts",
						initialDir,
						f,
						"--debug",
					)
					framework.ExpectNoError(err)

					foo, err := f.DevsySSH(ctx, tempDir, "cat $HOME/mnt1/foo.txt")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(foo)).To(gomega.Equal("BAR"))

					bar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/mnt2/bar.txt")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(bar)).To(gomega.Equal("FOO"))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should use custom image", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker",
						initialDir,
						f,
						"--devcontainer-image",
						"ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "grep ^ID= /etc/os-release")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "ID=alpine\n")
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should skip build with custom image", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-with-multi-stage-build",
						initialDir,
						f,
						"--devcontainer-image",
						"ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "grep ^ID= /etc/os-release")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "ID=alpine\n")
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))
			})
		})

		ginkgo.Context("with rootful podman", func() {
			var f *framework.Framework

			ginkgo.BeforeEach(func(ctx context.Context) {
				wrapper, err := os.Create(initialDir + "/bin/podman-rootful")
				framework.ExpectNoError(err)

				_, err = wrapper.WriteString("#!/bin/sh\nsudo podman \"$@\"\n")
				if err != nil {
					_ = wrapper.Close()
					framework.ExpectNoError(err)
				}

				err = wrapper.Close()
				framework.ExpectNoError(err)

				// #nosec G302 -- wrapper script needs execute permission
				err = os.Chmod(initialDir+"/bin/podman-rootful", 0o755)
				framework.ExpectNoError(err)

				err = exec.Command(initialDir+"/bin/podman-rootful", "ps").Run()
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func() {
					_ = os.Remove(initialDir + "/bin/podman-rootful")
				})

				f, err = setupDockerProvider(initialDir+"/bin", initialDir+"/bin/podman-rootful")
				framework.ExpectNoError(err)
			})

			ginkgo.Context("basic", func() {
				ginkgo.It(
					"should start a new workspace with existing image",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("build", func() {
				ginkgo.It(
					"should start a workspace with a multistage Dockerfile build",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-with-multi-stage-build",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)

				ginkgo.It(
					"should build and respect overrideCommand false",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-override-command-false",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("lifecycle commands", func() {
				ginkgo.It(
					"should run postCreateCommand with object syntax",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-postcreate-parallel",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						one, err := f.DevsySSH(ctx, tempDir, "cat /tmp/post-create-one.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(one)).To(gomega.Equal("postCreateOne"))

						two, err := f.DevsySSH(ctx, tempDir, "cat /tmp/post-create-two.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(two)).To(gomega.Equal("postCreateTwo"))
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It("should run postStartCommand after restart", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-post-start-restart",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/post-start-count.log")
					framework.ExpectNoError(err)
					lines := strings.Count(strings.TrimSpace(out), "\n") + 1
					gomega.Expect(lines).To(gomega.Equal(1),
						"postStartCommand should have run once after initial up")

					err = f.DevsyWorkspaceStop(ctx, tempDir)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir)
					framework.ExpectNoError(err)

					out, err = f.DevsySSH(ctx, tempDir, "cat $HOME/post-start-count.log")
					framework.ExpectNoError(err)
					lines = strings.Count(strings.TrimSpace(out), "\n") + 1
					gomega.Expect(lines).To(gomega.Equal(2),
						"postStartCommand should have run again after restart")
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It(
					"should defer postCreateCommand to background with waitFor",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-waitfor",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/on-create.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("onCreateDone"))

						out, err = f.DevsySSH(ctx, tempDir, "cat $HOME/update-content.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("updateContentDone"))

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/deferred.marker 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postCreateDone"),
						)

						envPath, err := f.DevsySSH(
							ctx, tempDir, "cat $HOME/deferred-env-path.out",
						)
						framework.ExpectNoError(err)
						gomega.Expect(envPath).To(
							gomega.ContainSubstring("/usr/local/bin"),
						)
						gomega.Expect(envPath).NotTo(gomega.ContainSubstring("${containerEnv:"))

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx,
								tempDir,
								"cat $HOME/post-start-deferred.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postStartDone"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should make IDE accessible before postAttachCommand completes",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-post-attach-nonblocking",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat $HOME/post-start.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("postStartDone"))

						_, err = f.DevsySSH(ctx, tempDir, "cat $HOME/post-attach.out")
						gomega.Expect(err).To(gomega.HaveOccurred())

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/post-attach.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(
							gomega.Equal("postAttachDone"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should run postAttachCommand on every attach",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-post-attach-every-time",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/attach-count.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(15 * time.Second).WithPolling(1 * time.Second).Should(
							gomega.Equal("1"),
						)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						gomega.Eventually(func() string {
							out, err := f.DevsySSH(
								ctx, tempDir, "cat $HOME/attach-count.out 2>/dev/null",
							)
							if err != nil {
								return ""
							}
							return strings.TrimSpace(out)
						}).WithTimeout(15 * time.Second).WithPolling(1 * time.Second).Should(
							gomega.Equal("2"),
						)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should run initializeCommand with object syntax",
					func(ctx context.Context) {
						tempDir, err := setupWorkspaceAndUp(
							ctx,
							"tests/up/testdata/docker-initcmd-parallel",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						one, err := os.ReadFile( //nolint:gosec // G304
							filepath.Join(tempDir, "init-cmd-one.out"),
						)
						framework.ExpectNoError(err)
						gomega.Expect(string(one)).To(gomega.Equal("initCmdOne"))

						two, err := os.ReadFile( //nolint:gosec // G304
							filepath.Join(tempDir, "init-cmd-two.out"),
						)
						framework.ExpectNoError(err)
						gomega.Expect(string(two)).To(gomega.Equal("initCmdTwo"))
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It(
					"should inject secrets-file env into lifecycle commands",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-secrets-file",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						secretsDir, err := framework.CreateTempDir()
						framework.ExpectNoError(err)
						ginkgo.DeferCleanup(func() { _ = os.RemoveAll(secretsDir) })

						secretsFile := filepath.Join(secretsDir, "secrets.env")
						err = os.WriteFile(
							secretsFile,
							[]byte("MY_SECRET=test-value-12345\nANOTHER_SECRET=second-secret-42\n"),
							0o600,
						)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir, "--secrets-file", secretsFile)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat /tmp/secret-check.out")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal("test-value-12345"))

						out, err = f.DevsySSH(
							ctx, tempDir, "cat /tmp/another-secret-check.out",
						)
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).
							To(gomega.Equal("second-secret-42"))
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("agent delivery", func() {
				ginkgo.It(
					"should deliver the agent binary and execute SSH commands",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						err = f.DevsySSHEchoTestString(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("exec", func() {
				ginkgo.It(
					"should execute commands inside the container via SSH",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace("tests/up/testdata/docker", initialDir, f)
						framework.ExpectNoError(err)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "echo -n hello-podman")
						framework.ExpectNoError(err)
						framework.ExpectEqual(out, "hello-podman")

						out, err = f.DevsySSH(ctx, tempDir, "pwd")
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(out)).NotTo(gomega.BeEmpty())
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("cleanup", func() {
				ginkgo.It(
					"should delete workspace and clean up resources",
					func(ctx context.Context) {
						tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker")
						framework.ExpectNoError(err)
						ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

						err = f.DevsyUp(ctx, tempDir)
						framework.ExpectNoError(err)

						_, err = f.FindWorkspace(ctx, tempDir)
						framework.ExpectNoError(err)

						err = f.DevsyWorkspaceDelete(ctx, tempDir)
						framework.ExpectNoError(err)

						_, err = f.FindWorkspace(ctx, tempDir)
						framework.ExpectError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)
			})

			ginkgo.Context("configuration", func() { //nolint:dupl
				ginkgo.It("should substitute variables", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-variables",
						initialDir,
						f,
						"--init-env", "CUSTOM_VAR=custom_value",
						"--init-env", "CUSTOM_IMAGE=ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					devContainerID, err := f.DevsySSH(
						ctx,
						tempDir,
						"cat $HOME/dev-container-id.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(devContainerID)).NotTo(gomega.BeEmpty())

					containerEnvPath, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-env-path.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(containerEnvPath).To(gomega.ContainSubstring("/usr/local/bin"))

					localEnvHome, err := f.DevsySSH(ctx, tempDir, "cat $HOME/local-env-home.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(localEnvHome)).
						To(gomega.Equal(os.Getenv("HOME")))

					localWorkspaceFolder, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/local-workspace-folder.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(
						framework.CleanString(strings.TrimSpace(localWorkspaceFolder)),
					).To(gomega.Equal(framework.CleanString(tempDir)))

					localWorkspaceFolderBasename, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/local-workspace-folder-basename.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(localWorkspaceFolderBasename)).
						To(gomega.Equal(filepath.Base(tempDir)))

					containerWorkspaceFolder, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-workspace-folder.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(
						framework.CleanString(strings.TrimSpace(containerWorkspaceFolder)),
					).To(gomega.Equal(
						framework.CleanString(path.Join("/workspaces", filepath.Base(tempDir))),
					))

					containerWorkspaceFolderBasename, err := f.DevsySSH(
						ctx, tempDir, "cat $HOME/container-workspace-folder-basename.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(containerWorkspaceFolderBasename)).
						To(gomega.Equal(filepath.Base(tempDir)))

					customVar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/custom-var.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(customVar)).To(gomega.Equal("custom_value"))

					customImage, err := f.DevsySSH(ctx, tempDir, "cat $HOME/custom-image.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(customImage)).
						To(gomega.Equal("ghcr.io/devsy-org/test-images/base:alpine"))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should substitute variables with defaults", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-variables-defaults",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					withDefault, err := f.DevsySSH(ctx, tempDir, "cat $HOME/with-default.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(withDefault)).
						To(gomega.Equal("my_default_value"))

					colonDefault, err := f.DevsySSH(ctx, tempDir, "cat $HOME/colon-default.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(colonDefault)).
						To(gomega.Equal("http://proxy:8080"))

					setVar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/set-var.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(setVar)).To(gomega.Equal(os.Getenv("HOME")))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should unset variable with remoteEnv null", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-remote-env-null",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					setLine, err := f.DevsySSH(ctx, tempDir, "head -1 $HOME/remote-env-null.out")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(setLine)).To(gomega.Equal("SET=hello"))

					unsetLine, err := f.DevsySSH(
						ctx, tempDir, "tail -1 $HOME/remote-env-null.out",
					)
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(unsetLine)).To(gomega.Equal("UNSET=true"))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should merge extra devcontainer config", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-extra-devcontainer",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					extraPath := path.Join(tempDir, "extra.json")
					err = f.DevsyUp(ctx, tempDir, "--extra-devcontainer-path", extraPath)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "bash -l -c 'echo -n $BASE_VAR'")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "base_value")

					out, err = f.DevsySSH(ctx, tempDir, "bash -l -c 'echo -n $EXTRA_VAR'")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "extra_value")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It(
					"should override with extra devcontainer config",
					func(ctx context.Context) {
						tempDir, err := setupWorkspace(
							"tests/up/testdata/docker-extra-override",
							initialDir,
							f,
						)
						framework.ExpectNoError(err)

						extraPath := path.Join(tempDir, "override.json")
						err = f.DevsyUp(ctx, tempDir, "--extra-devcontainer-path", extraPath)
						framework.ExpectNoError(err)

						out, err := f.DevsySSH(ctx, tempDir, "cat /tmp/test-var.out")
						framework.ExpectNoError(err)
						framework.ExpectEqual(strings.TrimSpace(out), "overridden_value")

						err = f.DevsyWorkspaceDelete(ctx, tempDir)
						framework.ExpectNoError(err)
					},
					ginkgo.SpecTimeout(framework.TimeoutShort()),
				)

				ginkgo.It("should select from multiple devcontainers", func(ctx context.Context) {
					tempDir, err := setupWorkspace(
						"tests/up/testdata/docker-multi-devcontainer",
						initialDir,
						f,
					)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir, "--devcontainer-id", "python")
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(
						ctx, tempDir, "bash -l -c 'echo -n $DEVCONTAINER_TYPE'",
					)
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "python")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)

					err = f.DevsyUp(ctx, tempDir, "--devcontainer-id", "go")
					framework.ExpectNoError(err)

					out, err = f.DevsySSH(
						ctx, tempDir, "bash -l -c 'echo -n $DEVCONTAINER_TYPE'",
					)
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "go")

					err = f.DevsyWorkspaceDelete(ctx, tempDir)
					framework.ExpectNoError(err)
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))
			})

			ginkgo.Context("features", func() {
				ginkgo.It("should mount volumes", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-mounts",
						initialDir,
						f,
						"--debug",
					)
					framework.ExpectNoError(err)

					foo, err := f.DevsySSH(ctx, tempDir, "cat $HOME/mnt1/foo.txt")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(foo)).To(gomega.Equal("BAR"))

					bar, err := f.DevsySSH(ctx, tempDir, "cat $HOME/mnt2/bar.txt")
					framework.ExpectNoError(err)
					gomega.Expect(strings.TrimSpace(bar)).To(gomega.Equal("FOO"))
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should use custom image", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker",
						initialDir,
						f,
						"--devcontainer-image",
						"ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "grep ^ID= /etc/os-release")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "ID=alpine\n")
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))

				ginkgo.It("should skip build with custom image", func(ctx context.Context) {
					tempDir, err := setupWorkspaceAndUp(
						ctx,
						"tests/up/testdata/docker-with-multi-stage-build",
						initialDir,
						f,
						"--devcontainer-image",
						"ghcr.io/devsy-org/test-images/base:alpine",
					)
					framework.ExpectNoError(err)

					out, err := f.DevsySSH(ctx, tempDir, "grep ^ID= /etc/os-release")
					framework.ExpectNoError(err)
					framework.ExpectEqual(out, "ID=alpine\n")
				}, ginkgo.SpecTimeout(framework.TimeoutShort()))
			})
		})
	},
)
