package up

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

// runFixtureCommand runs argv (argv[0] is the program), bounded by timeout. env entries
// are appended to the current environment.
func runFixtureCommand(
	ctx context.Context,
	timeout time.Duration,
	env []string,
	argv ...string,
) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// #nosec G204 -- executable and arguments are controlled by this test fixture.
	cmd := exec.CommandContext(commandCtx, argv[0], argv[1:]...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// fixtureCommand is runFixtureCommand that fails the current spec on error.
func fixtureCommand(
	ctx context.Context,
	timeout time.Duration,
	env []string,
	argv ...string,
) string {
	out, err := runFixtureCommand(ctx, timeout, env, argv...)
	gomega.Expect(err).
		NotTo(gomega.HaveOccurred(), "fixture command %v: %s", argv, out)
	return out
}

// fixtureWorkspacePod waits for exactly one ready workspace pod matching selector.
func fixtureWorkspacePod(ctx context.Context, namespace, selector string) corev1.Pod {
	var pod corev1.Pod
	gomega.Eventually(func(g gomega.Gomega) {
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, err := runFixtureCommand(
			pollCtx,
			10*time.Second,
			nil,
			"kubectl",
			"get",
			"pods",
			"-n",
			namespace,
			"-l",
			selector,
			"-o",
			"json",
		)
		g.Expect(err).NotTo(gomega.HaveOccurred(), "workspace pod discovery: %s", out)
		var pods corev1.PodList
		g.Expect(json.Unmarshal([]byte(out), &pods)).To(gomega.Succeed())
		g.Expect(pods.Items).To(gomega.HaveLen(1), "workspace-specific selector %s", selector)
		pod = pods.Items[0]
		g.Expect(pod.UID).NotTo(gomega.BeEmpty())
		ready := false
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		g.Expect(ready).To(gomega.BeTrue(), "workspace pod %s must be ready", pod.Name)
	}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
	ginkgo.GinkgoWriter.Printf("workspace pod %s UID=%s\n", pod.Name, pod.UID)
	return pod
}
