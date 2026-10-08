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
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const kubernetesIgnoreIDE = "none"

func writeKubernetesIgnoreFixture(root, name, content string) {
	file := filepath.Join(root, filepath.FromSlash(name))
	framework.ExpectNoError(os.MkdirAll(filepath.Dir(file), 0o750))
	framework.ExpectNoError(os.WriteFile(file, []byte(content), 0o600))
}

var _ = ginkgo.Describe(
	"workspace ignore policy in Kubernetes",
	ginkgo.Label("up-provider-kubernetes", "up-provider-kubernetes-ignore"),
	func() {
		ginkgo.DescribeTable(
			"streams workspace sources independently of additional mounts",
			func(ctx context.Context, dockerless, malformed bool) {
				initialDir, err := os.Getwd()
				framework.ExpectNoError(err)
				f := framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
				root, err := os.MkdirTemp("", "devsy-ignore-e2e-")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func() { framework.ExpectNoError(os.RemoveAll(root)) })
				workspace, other := filepath.Join(
					root,
					"workspace",
				), filepath.Join(
					root,
					"additional",
				)
				for _, name := range []string{
					"keep.txt", ".git/retained", "docs/a.md", "docs/a.tgz",
					"web/node_modules/lib/index.js", "build/keep.txt", "build/remove.txt",
					"private/.devsy-internal/secret.key",
				} {
					writeKubernetesIgnoreFixture(workspace, name, name)
				}
				writeKubernetesIgnoreFixture(other, "node_modules/lib/index.js", "independent")
				writeKubernetesIgnoreFixture(
					workspace,
					".devsyignore",
					"big/\n**/node_modules/\ndocs/*.tgz\nbuild/\n!build/keep.txt\n.devcontainer/\n**/.devsy-internal/\n",
				)
				// #nosec G304 -- workspace is a temporary fixture directory created by this test.
				blob, err := os.Create(filepath.Join(workspace, "blob.tmp"))
				framework.ExpectNoError(err)
				framework.ExpectNoError(blob.Truncate(20 * 1024 * 1024))
				framework.ExpectNoError(blob.Close())
				framework.ExpectNoError(os.MkdirAll(filepath.Join(workspace, "big"), 0o750))
				framework.ExpectNoError(
					os.Rename(
						filepath.Join(workspace, "blob.tmp"),
						filepath.Join(workspace, "big/blob.bin"),
					),
				)
				cfg := map[string]any{
					"image":           "ghcr.io/devsy-org/test-images/go:1",
					"workspaceFolder": "/workspaces/project",
					"workspaceMount":  "type=bind,source=${localWorkspaceFolder},target=/workspaces/project",
					"mounts": []string{
						"type=bind,source=" + other + ",target=/additional",
					},
				}
				if dockerless {
					delete(cfg, "image")
					cfg["build"] = map[string]string{"dockerfile": "Dockerfile", "context": "."}
					cfg["features"] = map[string]any{"./feature": map[string]any{}}
					writeKubernetesIgnoreFixture(
						workspace,
						".devcontainer/feature/devcontainer-feature.json",
						`{"id":"ignore-regression","version":"1.0.0","name":"Ignore regression"}`,
					)
					writeKubernetesIgnoreFixture(
						workspace,
						".devcontainer/feature/install.sh",
						"#!/bin/sh\nset -eu\ntest -L ./asset-link.txt\ntest -d ./required-empty\n"+
							"cat ./asset-link.txt > /tmp/devsy-ignore-feature-built\n",
					)
					writeKubernetesIgnoreFixture(
						workspace,
						".devcontainer/feature/asset.txt",
						"retained-feature-asset",
					)
					framework.ExpectNoError(
						os.Symlink(
							"asset.txt",
							filepath.Join(workspace, ".devcontainer/feature/asset-link.txt"),
						),
					)
					framework.ExpectNoError(
						os.MkdirAll(
							filepath.Join(workspace, ".devcontainer/feature/required-empty"),
							0o700,
						),
					)
					writeKubernetesIgnoreFixture(
						workspace,
						".devcontainer/Dockerfile",
						"FROM ghcr.io/devsy-org/test-images/go:1\nRUN touch /tmp/devsy-ignore-dockerless-built\n",
					)
				}
				data, err := json.Marshal(cfg)
				framework.ExpectNoError(err)
				writeKubernetesIgnoreFixture(
					workspace,
					".devcontainer/devcontainer.json",
					string(data),
				)

				namespace := fmt.Sprintf("devsy-ignore-%d", time.Now().UnixNano())
				framework.ExpectNoError(
					f.DevsyProviderAdd(ctx, "kubernetes", "-o", "KUBERNETES_NAMESPACE="+namespace),
				)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					// #nosec G204 -- namespace is a generated test name passed as one kubectl argument.
					cmd := exec.CommandContext(
						cleanupCtx, "kubectl", "delete", "namespace", namespace,
						"--ignore-not-found", "--timeout=60s",
					)
					framework.ExpectNoError(cmd.Run())
				})
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					framework.ExpectNoError(f.DevsyProviderDelete(cleanupCtx, "kubernetes"))
				})
				ginkgo.DeferCleanup(
					func(cleanupCtx context.Context) { framework.ExpectNoError(f.CleanupWorkspace(cleanupCtx, workspace)) },
				)
				if malformed {
					writeKubernetesIgnoreFixture(workspace, ".devsyignore", "[\n")
					_, stderr, upErr := f.ExecCommandCapture(
						ctx,
						[]string{
							"workspace",
							"up",
							"--debug",
							"--ide",
							kubernetesIgnoreIDE,
							workspace,
						},
					)
					gomega.Expect(upErr).To(gomega.HaveOccurred())
					gomega.Expect(stderr).To(gomega.ContainSubstring(".devsyignore"))
					return
				}
				framework.ExpectNoError(f.DevsyUp(ctx, workspace))
				checks := []string{
					"test -f keep.txt",
					"test -f .git/retained",
					"test -f docs/a.md",
					"test -f build/keep.txt",
					"test ! -e big/blob.bin",
					"test ! -e web/node_modules/lib/index.js",
					"test ! -e docs/a.tgz",
					"test ! -e build/remove.txt",
					"test ! -e private/.devsy-internal/secret.key",
					"test -f /additional/node_modules/lib/index.js",
					"test $(du -sk . | cut -f1) -lt 1024",
				}
				if dockerless {
					checks = append(
						checks,
						"test -f /tmp/devsy-ignore-dockerless-built",
						`test "$(cat /tmp/devsy-ignore-feature-built)" = retained-feature-asset`,
					)
				}
				output, err := f.DevsySSH(
					ctx,
					workspace,
					strings.Join(checks, " && ")+" && printf ignore-policy-ok",
				)
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(output)).To(gomega.Equal("ignore-policy-ok"))
			},
			ginkgo.Entry(
				"image workspace",
				false,
				false,
				ginkgo.SpecTimeout(framework.TimeoutModerate()),
			),
			ginkgo.Entry(
				"nested Dockerless build context",
				true,
				false,
				ginkgo.SpecTimeout(framework.TimeoutModerate()),
			),
			ginkgo.Entry(
				"malformed policy aborts workspace startup",
				false,
				true,
				ginkgo.SpecTimeout(framework.TimeoutModerate()),
			),
		)
	},
)
