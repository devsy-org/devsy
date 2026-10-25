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
	corev1 "k8s.io/api/core/v1"
)

const (
	inactivityLabel     = "up-provider-kubernetes-inactivity"
	inactivityImage     = "ghcr.io/devsy-org/test-images/go:1"
	inactivityPollEvery = 3 * time.Second
	sentinelName        = "idle-sentinel"
)

// idleEnv is one isolated namespace, provider and workspace.
type idleEnv struct {
	f    *framework.Framework
	ns   string
	dir  string
	wsID string
}

func newIdleEnv(ctx context.Context, initialDir string, providerOpts ...string) *idleEnv {
	e := &idleEnv{
		f:  framework.NewDefaultFramework(initialDir + "/bin"),
		ns: fmt.Sprintf("devsy-idle-%d", time.Now().UnixNano()%1_000_000_000),
	}
	e.dir = framework.CopyTestdata("tests/up/testdata/kubernetes", initialDir)

	opts := []string{
		"-o",
		"KUBERNETES_NAMESPACE=" + e.ns,
		"-o",
		"CREATE_NAMESPACE=true",
		"-o",
		"POD_TIMEOUT=5m",
	}
	for _, o := range providerOpts {
		opts = append(opts, "-o", o)
	}
	_ = e.f.DevsyProviderDelete(ctx, "kubernetes")
	framework.ExpectNoError(e.f.DevsyProviderAdd(ctx, append([]string{"kubernetes"}, opts...)...))

	ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
		c, cancel := framework.DetachedCleanupContext(cleanupCtx, 3*time.Minute)
		defer cancel()
		_ = e.f.DevsyWorkspaceDelete(c, e.dir)
		_ = e.f.DevsyProviderDelete(c, "kubernetes")
		_ = exec.CommandContext(c, "kubectl", "delete", "namespace", e.ns, // #nosec G204 -- test-owned namespace
			"--ignore-not-found", "--wait=false").
			Run()
	})
	return e
}

func (e *idleEnv) writeDevcontainer(content string) {
	dir := filepath.Join(e.dir, ".devcontainer")
	framework.ExpectNoError(os.MkdirAll(dir, 0o750))
	framework.ExpectNoError(
		os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(content), 0o600),
	)
}

func (e *idleEnv) up(ctx context.Context) error {
	return e.f.DevsyUp(ctx, e.dir)
}

func (e *idleEnv) workspaceID(ctx context.Context) string {
	if e.wsID == "" {
		ws, err := e.f.FindWorkspace(ctx, e.dir)
		framework.ExpectNoError(err)
		e.wsID = ws.ID
	}
	return e.wsID
}

func (e *idleEnv) kubectlJSON(ctx context.Context, into any, args ...string) error {
	full := append(append([]string{}, args...), "-n", e.ns, "-o", "json")
	cmd := exec.CommandContext(ctx, "kubectl", full...) // #nosec G204 -- test-owned kubectl args
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(out, into)
}

func (e *idleEnv) pod(ctx context.Context) (*corev1.Pod, error) {
	list := &corev1.PodList{}
	if err := e.kubectlJSON(ctx, list, "get", "pods", "-l", "devsy.sh/created=true"); err != nil {
		return nil, err
	}
	if len(list.Items) != 1 {
		return nil, fmt.Errorf("want exactly 1 workspace pod, got %d", len(list.Items))
	}
	return &list.Items[0], nil
}

func (e *idleEnv) pvcUID(ctx context.Context) string {
	list := &corev1.PersistentVolumeClaimList{}
	framework.ExpectNoError(e.kubectlJSON(ctx, list, "get", "pvc"))
	gomega.Expect(list.Items).To(gomega.HaveLen(1))
	return string(list.Items[0].UID)
}

func (e *idleEnv) ssh(ctx context.Context, command string) string {
	out, err := e.f.DevsySSH(ctx, e.dir, command)
	framework.ExpectNoError(err)
	return strings.TrimSpace(out)
}

// holdSSH keeps one tracked SSH session open until the returned stop is called.
func (e *idleEnv) holdSSH(ctx context.Context) (stop func()) {
	cmd := exec.CommandContext(
		ctx,
		filepath.Join(e.f.DevsyBinDir, e.f.DevsyBinName), // #nosec G204 -- test binary
		cmdWorkspace,
		cmdSSH,
		e.dir,
		flagCommand,
		"sleep 3600",
	)
	framework.ExpectNoError(cmd.Start())
	stop = func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	ginkgo.DeferCleanup(stop)
	return stop
}

func podTerminal(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

func devsyRestarts(pod *corev1.Pod) int32 {
	var n int32
	for _, c := range pod.Status.ContainerStatuses {
		n += c.RestartCount
	}
	return n
}

// waitStopped observes the pod through the Kubernetes API, never through
// workspace ssh, which would itself keep the workspace active.
func (e *idleEnv) waitStopped(ctx context.Context, uid string, within time.Duration) *corev1.Pod {
	var last *corev1.Pod
	gomega.Eventually(func() bool {
		pod, err := e.pod(ctx)
		if err != nil {
			return false
		}
		last = pod
		return string(pod.UID) == uid && podTerminal(pod)
	}, within, inactivityPollEvery).Should(gomega.BeTrue(), "workspace pod should reach a terminal phase")

	gomega.Expect(last.Status.Phase).ToNot(gomega.Equal(corev1.PodRunning))
	gomega.Expect(devsyRestarts(last)).To(gomega.BeZero(), "the stopped pod must not restart")
	for _, c := range last.Status.ContainerStatuses {
		gomega.Expect(c.State.Terminated).
			ToNot(gomega.BeNil(), "container %s should be terminated", c.Name)
	}
	return last
}

// expectRunningFor asserts the same pod instance keeps running for the whole window.
func (e *idleEnv) expectRunningFor(ctx context.Context, uid string, window time.Duration) {
	gomega.Consistently(func() string {
		pod, err := e.pod(ctx)
		if err != nil || string(pod.UID) != uid {
			return "missing"
		}
		return string(pod.Status.Phase)
	}, window, inactivityPollEvery).Should(gomega.Equal(string(corev1.PodRunning)))
}

func devcontainerWith(extra string) string {
	return fmt.Sprintf(`{"name":"idle","image":%q%s}`, inactivityImage, extra)
}

const sidecarTemplate = "spec:\n  containers:\n  - name: sidecar\n    image: " + inactivityImage +
	"\n    command: [sleep, infinity]\n"

const nativeSidecarTemplate = "spec:\n  initContainers:\n  - name: mesh\n    image: " + inactivityImage +
	"\n    command: [sleep, infinity]\n    restartPolicy: Always\n"

var _ = ginkgo.Describe(
	"kubernetes workspace inactivity timeout",
	ginkgo.Label(inactivityLabel),
	ginkgo.Serial,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"stops an idle workspace and resumes it with the volume intact",
			func(ctx context.Context) {
				e := newIdleEnv(ctx, initialDir, "INACTIVITY_TIMEOUT=1m")
				framework.ExpectNoError(e.up(ctx))

				pod, err := e.pod(ctx)
				framework.ExpectNoError(err)
				gomega.Expect(pod.Spec.Containers).To(gomega.HaveLen(1))
				pvc := e.pvcUID(ctx)
				workspaceUID := e.workspaceUID(ctx)
				sentinel := "/workspaces/" + e.workspaceID(ctx) + "/" + sentinelName

				for cycle := 1; cycle <= 2; cycle++ {
					ginkgo.By(fmt.Sprintf("cycle %d: writing a sentinel and going idle", cycle))
					e.ssh(ctx, fmt.Sprintf("echo cycle-%d > %s", cycle, sentinel))
					podUID := string(pod.UID)

					e.waitStopped(ctx, podUID, 4*time.Minute)
					gomega.Expect(e.pvcUID(ctx)).To(gomega.Equal(pvc))

					ginkgo.By(fmt.Sprintf("cycle %d: resuming with a plain up", cycle))
					framework.ExpectNoError(e.up(ctx))
					pod, err = e.pod(ctx)
					framework.ExpectNoError(err)
					gomega.Expect(string(pod.UID)).
						ToNot(gomega.Equal(podUID), "a new pod replaced the stopped one")
					gomega.Expect(pod.Status.Phase).To(gomega.Equal(corev1.PodRunning))
					gomega.Expect(e.pvcUID(ctx)).To(gomega.Equal(pvc))
					gomega.Expect(e.workspaceUID(ctx)).To(gomega.Equal(workspaceUID))
					gomega.Expect(e.ssh(ctx, "cat "+sentinel)).
						To(gomega.Equal(fmt.Sprintf("cycle-%d", cycle)))
				}
			},
			ginkgo.SpecTimeout(framework.TimeoutVeryLong()),
		)

		ginkgo.It("stays running while a tracked SSH session is open and stops after it ends",
			func(ctx context.Context) {
				e := newIdleEnv(ctx, initialDir, "INACTIVITY_TIMEOUT=30s")
				framework.ExpectNoError(e.up(ctx))
				pod, err := e.pod(ctx)
				framework.ExpectNoError(err)

				stop := e.holdSSH(ctx)
				e.expectRunningFor(ctx, string(pod.UID), 75*time.Second)

				stop()
				e.waitStopped(ctx, string(pod.UID), 3*time.Minute)
			}, ginkgo.SpecTimeout(framework.TimeoutLong()))

		ginkgo.It("does not stop when shutdownAction is none", func(ctx context.Context) {
			e := newIdleEnv(ctx, initialDir, "INACTIVITY_TIMEOUT=30s")
			e.writeDevcontainer(devcontainerWith(`,"shutdownAction":"none"`))
			framework.ExpectNoError(e.up(ctx))
			pod, err := e.pod(ctx)
			framework.ExpectNoError(err)

			e.expectRunningFor(ctx, string(pod.UID), 90*time.Second)
		}, ginkgo.SpecTimeout(framework.TimeoutLong()))

		ginkgo.It("does not stop when the timeout is unset", func(ctx context.Context) {
			e := newIdleEnv(ctx, initialDir)
			framework.ExpectNoError(e.up(ctx))
			pod, err := e.pod(ctx)
			framework.ExpectNoError(err)

			e.expectRunningFor(ctx, string(pod.UID), 75*time.Second)
		}, ginkgo.SpecTimeout(framework.TimeoutLong()))

		ginkgo.It(
			"keeps the workspace alive during a long postAttachCommand",
			func(ctx context.Context) {
				e := newIdleEnv(ctx, initialDir, "INACTIVITY_TIMEOUT=30s")
				e.writeDevcontainer(devcontainerWith(`,"postAttachCommand":"sleep 80"`))
				framework.ExpectNoError(e.up(ctx))
				pod, err := e.pod(ctx)
				framework.ExpectNoError(err)

				e.expectRunningFor(ctx, string(pod.UID), 60*time.Second)
				e.waitStopped(ctx, string(pod.UID), 4*time.Minute)
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)

		ginkgo.It(
			"recreates a terminal pod with unchanged options on the next up",
			func(ctx context.Context) {
				e := newIdleEnv(ctx, initialDir)
				framework.ExpectNoError(e.up(ctx))
				pod, err := e.pod(ctx)
				framework.ExpectNoError(err)
				pvc := e.pvcUID(ctx)

				out, err := exec.CommandContext(ctx, "kubectl", "exec", "-n", e.ns, pod.Name, // #nosec G204 -- test pod
					"-c", "devsy", "--", "sh", "-c", "kill -TERM 1").
					CombinedOutput()
				framework.ExpectNoError(err, string(out))
				e.waitStopped(ctx, string(pod.UID), 2*time.Minute)

				framework.ExpectNoError(e.up(ctx))
				fresh, err := e.pod(ctx)
				framework.ExpectNoError(err)
				gomega.Expect(fresh.UID).ToNot(gomega.Equal(pod.UID))
				gomega.Expect(e.pvcUID(ctx)).To(gomega.Equal(pvc))
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)

		ginkgo.It(
			"stops the whole pod when a native sidecar is present",
			func(ctx context.Context) {
				e := newIdleEnv(
					ctx,
					initialDir,
					"INACTIVITY_TIMEOUT=30s",
					"POD_MANIFEST_TEMPLATE="+nativeSidecarTemplate,
				)
				framework.ExpectNoError(e.up(ctx))
				pod, err := e.pod(ctx)
				framework.ExpectNoError(err)

				stopped := e.waitStopped(ctx, string(pod.UID), 4*time.Minute)
				for _, c := range stopped.Status.InitContainerStatuses {
					gomega.Expect(c.State.Terminated).
						ToNot(gomega.BeNil(), "sidecar %s should end with the pod", c.Name)
				}
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)

		ginkgo.It(
			"rejects extra regular containers only when a timeout is enabled",
			func(ctx context.Context) {
				e := newIdleEnv(
					ctx,
					initialDir,
					"INACTIVITY_TIMEOUT=30s",
					"POD_MANIFEST_TEMPLATE="+sidecarTemplate,
				)

				_, stderr, err := e.f.ExecCommandCapture(ctx,
					[]string{cmdWorkspace, "up", e.dir, "--ide", "none"})
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(stderr).To(gomega.ContainSubstring("sidecar"))
				pvcs := &corev1.PersistentVolumeClaimList{}
				framework.ExpectNoError(e.kubectlJSON(ctx, pvcs, "get", "pvc"))
				gomega.Expect(pvcs.Items).To(gomega.BeEmpty(), "nothing is created for a rejected pod")

				framework.ExpectNoError(e.f.DevsyWorkspaceDelete(ctx, e.dir))
				framework.ExpectNoError(os.Chdir(initialDir))
				e = newIdleEnv(
					ctx,
					initialDir,
					"INACTIVITY_TIMEOUT=0",
					"POD_MANIFEST_TEMPLATE="+sidecarTemplate,
				)
				framework.ExpectNoError(e.up(ctx))
				pod, err := e.pod(ctx)
				framework.ExpectNoError(err)
				gomega.Expect(pod.Spec.Containers).To(gomega.HaveLen(2))
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)

		ginkgo.It(
			"stops and resumes under a restricted non-root security context",
			func(ctx context.Context) {
				framework.ExpectNoError(labelNamespaceRestricted(ctx))
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = exec.CommandContext(cleanupCtx, "kubectl", "delete", "namespace", restrictedNamespace,
						"--ignore-not-found", "--wait=false").
						Run()
				})
				e := newIdleEnv(ctx, initialDir,
					"INACTIVITY_TIMEOUT=30s",
					"STRICT_SECURITY=true",
					"AGENT_SECURITY_CONTEXT="+restrictedSecurityContextYAML,
					"POD_MANIFEST_TEMPLATE="+restrictedPodManifestTemplate,
					"AGENT_INSTALL_PATH=/tmp/devsy",
				)
				e.ns = restrictedNamespace
				framework.ExpectNoError(e.f.DevsyProviderUse(
					ctx,
					"kubernetes",
					"-o",
					"KUBERNETES_NAMESPACE="+restrictedNamespace,
					"-o",
					"CREATE_NAMESPACE=false",
				))

				framework.ExpectNoError(e.up(ctx))
				pod, err := e.pod(ctx)
				framework.ExpectNoError(err)
				e.waitStopped(ctx, string(pod.UID), 4*time.Minute)

				framework.ExpectNoError(e.up(ctx))
				fresh, err := e.pod(ctx)
				framework.ExpectNoError(err)
				gomega.Expect(fresh.UID).ToNot(gomega.Equal(pod.UID))
			},
			ginkgo.SpecTimeout(framework.TimeoutVeryLong()),
		)
	},
)

func (e *idleEnv) workspaceUID(ctx context.Context) string {
	ws, err := e.f.FindWorkspace(ctx, e.dir)
	framework.ExpectNoError(err)
	return ws.UID
}
