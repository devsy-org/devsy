package up

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const restrictedNamespace = "devsy-restricted"

const restrictedSecurityContextYAML = "runAsUser: 1000\n" +
	"runAsGroup: 1000\n" +
	"runAsNonRoot: true\n" +
	"allowPrivilegeEscalation: false\n" +
	"seccompProfile:\n" +
	"  type: RuntimeDefault\n" +
	"capabilities:\n" +
	"  drop: [\"ALL\"]\n"

const restrictedPodManifestTemplate = "spec:\n  hostUsers: true\n"

func labelNamespaceRestricted(ctx context.Context) error {
	createOrUpdate := fmt.Sprintf(
		"kubectl create namespace %s --dry-run=client -o yaml | kubectl apply -f -",
		restrictedNamespace,
	)
	// #nosec G204 -- createOrUpdate is built from the fixed restrictedNamespace const
	if err := exec.CommandContext(ctx, "sh", "-c", createOrUpdate).Run(); err != nil {
		return err
	}
	return exec.CommandContext(
		ctx, "kubectl", "label", "namespace", restrictedNamespace,
		"pod-security.kubernetes.io/enforce=restricted",
		"pod-security.kubernetes.io/enforce-version=latest",
		"--overwrite",
	).Run()
}

var _ = ginkgo.Describe(
	"testing up command for kubernetes provider under Pod Security Admission restricted",
	ginkgo.Label("up-provider-kubernetes-restricted-scc"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)

			err = labelNamespaceRestricted(context.Background())
			framework.ExpectNoError(err)
		})

		ginkgo.AfterEach(func() {
			_ = exec.Command("kubectl", "delete", "namespace", restrictedNamespace, "--ignore-not-found").
				Run()
		})

		ginkgo.It(
			"rejects the default root security context and succeeds with AGENT_SECURITY_CONTEXT + STRICT_SECURITY",
			func(ctx context.Context) {
				f := framework.NewDefaultFramework(initialDir + "/bin")
				tempDir, err := framework.CopyToTempDir("tests/up/testdata/kubernetes")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

				_ = f.DevsyProviderDelete(ctx, "kubernetes")
				err = f.DevsyProviderAdd(
					ctx, "kubernetes",
					"-o", "KUBERNETES_NAMESPACE="+restrictedNamespace,
					"-o", "CREATE_NAMESPACE=false",
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err := f.DevsyProviderDelete(cleanupCtx, "kubernetes")
					framework.ExpectNoError(err)
				})

				ginkgo.By("rejecting the default root security context under admission")
				err = f.DevsyUp(ctx, tempDir)
				gomega.Expect(err).To(gomega.HaveOccurred())

				ginkgo.By("switching to a restricted-admission security context")
				err = f.DevsyProviderUse(
					ctx, "kubernetes",
					"-o", "STRICT_SECURITY=true",
					"-o", "AGENT_SECURITY_CONTEXT="+restrictedSecurityContextYAML,
					"-o", "POD_MANIFEST_TEMPLATE="+restrictedPodManifestTemplate,
					"-o", "AGENT_INSTALL_PATH=/tmp/devsy",
				)
				framework.ExpectNoError(err)

				ginkgo.By("admitting and running a non-root workspace")
				err = f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, tempDir)

				list := waitForPodCount(ctx, restrictedNamespace, 1, "Expect 1 pod")
				// PSA does not require user namespaces. The explicit template
				// override keeps this admission test runnable on Kind nodes where
				// the outer container runtime cannot create nested user namespaces.
				gomega.Expect(list.Items[0].Spec.HostUsers).ToNot(gomega.BeNil())
				gomega.Expect(*list.Items[0].Spec.HostUsers).To(gomega.BeTrue())
				sc := list.Items[0].Spec.Containers[0].SecurityContext
				gomega.Expect(sc).ToNot(gomega.BeNil())
				gomega.Expect(*sc.RunAsUser).To(gomega.Equal(int64(1000)))
				gomega.Expect(*sc.RunAsNonRoot).To(gomega.BeTrue())

				err = f.ExecCommand(
					ctx,
					true,
					true,
					"mYtEsTsTrInG",
					[]string{
						"workspace",
						"ssh",
						"--agent-forwarding=false",
						"--command",
						"echo 'bVl0RXNUc1RySW5H' | base64 -d",
						tempDir,
					},
				)
				framework.ExpectNoError(err)
			},
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
		)
	},
)
