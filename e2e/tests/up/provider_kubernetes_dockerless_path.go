package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver/kubernetes"
	"github.com/devsy-org/devsy/pkg/flags/names"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

const (
	dockerlessPathRoot  = "/opt/devsy-dockerless-path"
	dockerlessImagePath = dockerlessPathRoot + "/bin:/usr/local/sbin:/usr/local/bin:" +
		"/usr/sbin:/usr/bin:/sbin:/bin"
	dockerlessImageDockerConfig = dockerlessPathRoot + "/docker-config"
	dockerlessPathCommand       = "devsy-dockerless-path-check"
)

type dockerlessPathFixture struct {
	framework       *framework.Framework
	id              string
	dir             string
	expectedPath    string
	credentialsDirs []string
	containerName   string
}

func init() {
	ginkgo.Describe("Kubernetes Dockerless image environment",
		ginkgo.Label("up-provider-kubernetes", "up-provider-kubernetes-dockerless-path"),
		func() {
			ginkgo.DescribeTable(
				"preserves image PATH after credentials cleanup",
				runDockerlessPathFixture,
				ginkgo.Entry("default remote PATH", false,
					ginkgo.SpecTimeout(framework.TimeoutLong())),
				ginkgo.Entry("remoteEnv appends to image PATH", true,
					ginkgo.SpecTimeout(framework.TimeoutLong())),
			)
		},
	)
}

func runDockerlessPathFixture(ctx ginkgo.SpecContext, appendRemotePath bool) {
	fixture := newDockerlessPathFixture(ctx, appendRemotePath)
	fixture.up(ctx, "create", true, fixture.dir, "--provider", fixture.id, "--id", fixture.id)
	firstPod := fixture.verify(ctx, "create")
	// Repeated setup currently substitutes containerEnv from the persisted
	// remote environment, so appending PATH is checked on a fresh build.
	if appendRemotePath {
		return
	}
	fixture.up(ctx, "repeat", false, fixture.id)
	repeatedPod := fixture.verify(ctx, "repeat")
	gomega.Expect(repeatedPod.UID).
		To(gomega.Equal(firstPod.UID), "repeat up must keep the pod")
	fixture.up(ctx, "recreate", true, fixture.id, "--recreate")
	recreatedPod := fixture.verify(ctx, "recreate")
	gomega.Expect(recreatedPod.UID).
		NotTo(gomega.Equal(firstPod.UID), "recreate must replace the pod")
	framework.ExpectNoError(fixture.framework.DevsyWorkspaceStop(ctx, fixture.id))
	waitForPodCount(ctx, fixture.id, 0, "stop must remove the workspace pod")
	fixture.up(ctx, "restart", false, fixture.id)
	restartedPod := fixture.verify(ctx, "restart")
	gomega.Expect(restartedPod.UID).
		NotTo(gomega.Equal(recreatedPod.UID), "restart must create a new pod")
}

func newDockerlessPathFixture(ctx context.Context, appendRemotePath bool) *dockerlessPathFixture {
	initialDir, err := os.Getwd()
	framework.ExpectNoError(err)
	fixtureDir, err := framework.CopyToTempDir("tests/up/testdata/kubernetes-dockerless-path")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, fixtureDir)
	fixture := &dockerlessPathFixture{
		framework:    framework.NewDefaultFramework(filepath.Join(initialDir, "bin")),
		id:           fmt.Sprintf("dlpath-%d", time.Now().UnixNano()),
		dir:          fixtureDir,
		expectedPath: dockerlessImagePath,
	}
	if appendRemotePath {
		fixture.expectedPath += ":" + dockerlessPathRoot + "/remote-bin"
		addDockerlessRemotePath(fixtureDir)
	}
	dockerlessPathKubectl(ctx, "create", "namespace", fixture.id)
	ginkgo.DeferCleanup(fixture.deleteNamespace)
	// Keep the built-in provider's Dockerless credentials enabled.
	// Debug output records the helper created by the real callback.
	framework.ExpectNoError(fixture.framework.DevsyProviderAdd(ctx, "kubernetes",
		"--name", fixture.id, "-o", "KUBERNETES_NAMESPACE="+fixture.id,
		"-o", "LABELS=issue-1446="+fixture.id))
	ginkgo.DeferCleanup(fixture.deleteProvider)
	ginkgo.DeferCleanup(func(specCtx ginkgo.SpecContext) {
		framework.ExpectNoError(fixture.framework.CleanupWorkspace(specCtx, fixture.id))
	})
	return fixture
}

func (fixture *dockerlessPathFixture) deleteNamespace(specCtx ginkgo.SpecContext) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(specCtx), time.Minute)
	defer cancel()
	dockerlessPathKubectl(cleanupCtx, "delete", "namespace", fixture.id, "--wait=false")
}

func (fixture *dockerlessPathFixture) deleteProvider(specCtx ginkgo.SpecContext) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(specCtx), time.Minute)
	defer cancel()
	framework.ExpectNoError(fixture.framework.DevsyProviderDelete(cleanupCtx, fixture.id))
}

func (fixture *dockerlessPathFixture) up(
	ctx context.Context,
	phase string,
	mustBuild bool,
	args ...string,
) {
	commandCtx, cancel := context.WithTimeout(ctx, framework.TimeoutModerate())
	defer cancel()
	// Single CLI attempt: a second up must not repair first-up failure.
	baseArgs := []string{
		cmdWorkspace,
		"up",
		names.Flag(names.Debug),
		names.Flag(names.IDE),
		kubernetesIgnoreIDE,
	}
	stdout, stderr, upErr := fixture.framework.ExecCommandCapture(
		commandCtx,
		append(baseArgs, args...),
	)
	gomega.Expect(upErr).NotTo(gomega.HaveOccurred(),
		"%s first CLI attempt: stdout=%s stderr=%s", phase, stdout, stderr)
	output := stdout + stderr
	gomega.Expect(output).NotTo(gomega.ContainSubstring("failed to configure docker credentials"))
	gomega.Expect(output).NotTo(gomega.ContainSubstring("docker credentials disabled"))
	helperPattern := regexp.MustCompile(
		`Wrote docker credentials helper to ` +
			`(/\.dockerless/\.docker/docker-credentials-[a-zA-Z0-9]{12})/`,
	)
	helpers := helperPattern.FindAllStringSubmatch(output, -1)
	for _, helper := range helpers {
		fixture.credentialsDirs = append(fixture.credentialsDirs, helper[1])
	}
	if mustBuild {
		gomega.Expect(helpers).
			NotTo(gomega.BeEmpty(), "%s must configure Dockerless credentials", phase)
		gomega.Expect(output).To(gomega.ContainSubstring("starting dockerless build"), phase)
		gomega.Expect(output).To(gomega.ContainSubstring("dockerless build completed"), phase)
	}
}

func (fixture *dockerlessPathFixture) verify(ctx context.Context, phase string) corev1.Pod {
	selector := "devsy.sh/created=true,issue-1446=" + fixture.id
	pod := dockerlessPathWorkspacePod(ctx, fixture.id, selector)
	fixture.containerName = ""
	for _, container := range pod.Spec.Containers {
		if container.Name == kubernetes.DevContainerName {
			fixture.containerName = container.Name
		}
	}
	gomega.Expect(fixture.containerName).
		NotTo(gomega.BeEmpty(), "workspace container in pod %s", pod.Name)
	fixture.verifyEnvironment(ctx, pod.Name, phase)
	return pod
}

func addDockerlessRemotePath(fixtureDir string) {
	configPath := filepath.Join(fixtureDir, ".devcontainer", "devcontainer.json")
	// #nosec G304 -- fixed filename in a test-owned temporary fixture directory.
	raw, err := os.ReadFile(configPath)
	framework.ExpectNoError(err)
	var cfg map[string]any
	framework.ExpectNoError(json.Unmarshal(raw, &cfg))
	remoteEnv, ok := cfg["remoteEnv"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue())
	remoteEnv["PATH"] = "${containerEnv:PATH}:" + dockerlessPathRoot + "/remote-bin"
	raw, err = json.MarshalIndent(cfg, "", "  ")
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(configPath, raw, 0o600))
}

func (fixture *dockerlessPathFixture) verifyEnvironment(
	ctx context.Context,
	podName, phase string,
) {
	commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	expectedOutput := fmt.Sprintf(
		"command=%s/bin/%s\nPATH=%s\nDOCKER_CONFIG=%s",
		dockerlessPathRoot,
		dockerlessPathCommand,
		fixture.expectedPath,
		dockerlessImageDockerConfig,
	)
	// No login/profile probe, no command retry, and no absolute executable path:
	// SSH must find the executable solely through the image/remote environment.
	out, err := fixture.framework.DevsySSHOnce(commandCtx, fixture.id, dockerlessPathCommand)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "%s bare-name SSH command", phase)
	gomega.Expect(strings.TrimSpace(out)).
		To(gomega.Equal(expectedOutput), "%s SSH environment", phase)
	gomega.Expect(out).NotTo(gomega.ContainSubstring("/.dockerless"))

	// Raw kubectl exec reads files without treating the pod's original builder
	// environment as the post-setup agent/SSH environment.
	execInPod := func(args ...string) string {
		baseArgs := []string{"exec", "-n", fixture.id, podName, "-c", fixture.containerName, "--"}
		return dockerlessPathKubectl(commandCtx, append(baseArgs, args...)...)
	}
	readFile := func(name string) string {
		return execInPod("/bin/cat", name)
	}
	gomega.Expect(strings.TrimSpace(readFile(dockerlessPathRoot+"/post-start.env"))).
		To(gomega.Equal(expectedOutput), "%s postStartCommand environment", phase)
	var image v1.ConfigFile
	framework.ExpectNoError(json.Unmarshal([]byte(readFile("/.dockerless/image.json")), &image))
	gomega.Expect(image.Config.Env).To(gomega.ContainElement("PATH=" + dockerlessImagePath))
	gomega.Expect(image.Config.Env).
		To(gomega.ContainElement("DOCKER_CONFIG=" + dockerlessImageDockerConfig))
	var persisted struct {
		Env map[string]string `json:"env"`
	}
	framework.ExpectNoError(json.Unmarshal([]byte(readFile("/etc/envfile.json")), &persisted))
	gomega.Expect(persisted.Env["PATH"]).
		To(gomega.Equal(fixture.expectedPath), "%s persisted remote PATH", phase)
	gomega.Expect(persisted.Env["DOCKER_CONFIG"]).
		To(gomega.Equal(dockerlessImageDockerConfig), phase)
	for _, credentialsDir := range fixture.credentialsDirs {
		execInPod("/bin/test", "!", "-e", credentialsDir)
	}
	execInPod("/bin/sh", "-c",
		`for dir in /.dockerless/.docker/docker-credentials-*; do test ! -e "$dir" || exit 1; done`)
}

func dockerlessPathWorkspacePod(ctx context.Context, namespace, selector string) corev1.Pod {
	var pod corev1.Pod
	gomega.Eventually(func(g gomega.Gomega) {
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, err := runDockerlessPathKubectl(
			pollCtx,
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
		g.Expect(pods.Items).To(gomega.HaveLen(1))
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
	return pod
}

func runDockerlessPathKubectl(ctx context.Context, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// #nosec G204 -- kubectl arguments are controlled by this test fixture.
	cmd := exec.CommandContext(commandCtx, "kubectl", args...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func dockerlessPathKubectl(ctx context.Context, args ...string) string {
	out, err := runDockerlessPathKubectl(ctx, args...)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "kubectl %v: %s", args, out)
	return out
}
