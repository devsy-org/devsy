package up

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/pkg/driver/kubernetes"
	devsyencoding "github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
)

const (
	imageConfigContainerUser      = "containerUser"
	imageConfigRemoteUser         = "remoteUser"
	imageConfigUIDFlag            = "--uid"
	imageConfigKubectl            = "kubectl"
	imageConfigLogMarker          = "image-config-fixture-log-marker"
	imageConfigDaemonSecretPrefix = kubernetes.DevContainerName + "-daemon-secret-" + kubernetes.DevContainerName + "-"
)

const imageConfigRegistryImage = "registry@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373"

type imageConfigFixture struct {
	f                                                                                 *framework.Framework
	admin, namespace, registry, authority, image, file, dir, id, node, root, password string
	uid, controlUID, configDigest                                                     string
}

type imageConfigOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (o *imageConfigOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.Write(data)
}

type imageConfigLogObserver struct {
	mu     sync.Mutex
	tail   []byte
	seen   bool
	cancel context.CancelFunc
}

func (o *imageConfigLogObserver) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.seen {
		return len(data), nil
	}
	o.tail = append(o.tail, data...)
	window := o.tail
	if bytes.Contains(window, []byte(imageConfigLogMarker)) {
		o.seen = true
		o.cancel()
	}
	keep := len(imageConfigLogMarker) - 1
	if len(window) > keep {
		window = window[len(window)-keep:]
	}
	o.tail = append(o.tail[:0], window...)
	return len(data), nil
}

func (x *imageConfigFixture) observeLogs(ctx context.Context) {
	logsCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	observer := &imageConfigLogObserver{cancel: cancel}
	result, err := (runenv.ExecRunner{}).Run(logsCtx, runenv.Command{
		Path:               filepath.Join(x.f.DevsyBinDir, x.f.DevsyBinName),
		Args:               []string{cmdWorkspace, "logs", x.id},
		Stdout:             observer,
		CancelProcessGroup: true,
	})
	observer.mu.Lock()
	seen := observer.seen
	observer.mu.Unlock()
	gomega.Expect(seen).To(gomega.BeTrue(), "workspace logs must contain the fixture marker")
	gomega.Expect(logsCtx.Err()).To(gomega.MatchError(context.Canceled))
	gomega.Expect(result.TimedOut).To(gomega.BeFalse())
	gomega.Expect(result.ExitCode != 0 || errors.Is(err, context.Canceled)).To(gomega.BeTrue())
}

func imageConfigCommand(
	ctx context.Context, env []string, program string, args ...string,
) (string, error) {
	switch program {
	case testDockerCommand, imageConfigKubectl, "kind":
	default:
		return "", fmt.Errorf("unsupported fixture command %q", program)
	}
	output := &imageConfigOutput{}
	result, err := (runenv.ExecRunner{}).Run(ctx, runenv.Command{
		Path: program, Args: args, Env: env, Stdout: output, Stderr: output,
	})
	out := strings.TrimSpace(output.buffer.String())
	if err != nil {
		return out, fmt.Errorf("run %s: %w", program, err)
	}
	if result.TimedOut || result.ExitCode != 0 {
		return out, fmt.Errorf("%s exited with code %d: %s", program, result.ExitCode, out)
	}
	return out, nil
}

func imageConfigMust(ctx context.Context, env []string, program string, args ...string) string {
	out, err := imageConfigCommand(ctx, env, program, args...)
	framework.ExpectNoError(err)
	return out
}

func (x *imageConfigFixture) kubectl(ctx context.Context, args ...string) string {
	env := append(os.Environ(), "KUBECONFIG="+x.admin)
	return imageConfigMust(ctx, env, imageConfigKubectl, args...)
}

func imageConfigWrite(path string, value any) {
	data, err := json.Marshal(value)
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(path, data, 0o600))
}

func newImageConfigFixture(ctx context.Context) *imageConfigFixture {
	root := ginkgo.GinkgoT().TempDir()
	cwd, err := os.Getwd()
	framework.ExpectNoError(err)
	x := &imageConfigFixture{
		f:     framework.NewDefaultFramework(filepath.Join(cwd, "bin")),
		admin: os.Getenv("KUBECONFIG"),
		id:    fmt.Sprintf("cfg%d", time.Now().UnixNano()),
	}
	if x.admin == "" {
		x.admin = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}
	x.root = root
	x.uid = devsyencoding.CreateNewUID("", x.id)
	x.controlUID = devsyencoding.CreateNewUID("", x.id+"control")
	x.namespace = x.id
	x.registry = "devsy-registry-" + x.id
	clusterContext, arch := x.cluster(ctx)
	x.startRegistry(ctx)
	x.publishImage(ctx, arch)
	x.provisionIdentity(ctx)
	x.configureClient(ctx, clusterContext)
	x.configureProvider(ctx, arch)
	return x
}

func (x *imageConfigFixture) cluster(ctx context.Context) (string, string) {
	clusterContext := x.kubectl(ctx, "config", "current-context")
	gomega.Expect(clusterContext).To(gomega.HavePrefix("kind-"))
	nodes := strings.Fields(
		imageConfigMust(
			ctx,
			nil,
			"kind",
			"get",
			"nodes",
			"--name",
			strings.TrimPrefix(clusterContext, "kind-"),
		),
	)
	gomega.Expect(nodes).To(gomega.HaveLen(1))
	x.node = nodes[0]
	arch := x.kubectl(ctx, "get", "node", x.node, "-o", "jsonpath={.status.nodeInfo.architecture}")
	gomega.Expect(arch).To(gomega.Equal(runtime.GOARCH))
	return clusterContext, arch
}

func (x *imageConfigFixture) startRegistry(ctx context.Context) {
	root := x.root
	password := "private-" + x.id
	x.password = password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	framework.ExpectNoError(err)
	authDir := filepath.Join(root, "auth")
	framework.ExpectNoError(os.MkdirAll(authDir, 0o700))
	framework.ExpectNoError(
		os.WriteFile(
			filepath.Join(authDir, "htpasswd"),
			[]byte("fixture:"+string(hash)+"\n"),
			0o600,
		),
	)
	imageConfigMust(
		ctx,
		nil,
		testDockerCommand,
		"run",
		"-d",
		"--name",
		x.registry,
		"--network",
		"kind",
		"-p",
		"127.0.0.1::5000",
		"-v",
		authDir+":/auth:ro",
		"-e",
		"REGISTRY_AUTH=htpasswd",
		"-e",
		"REGISTRY_AUTH_HTPASSWD_REALM=fixture",
		"-e",
		"REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd",
		imageConfigRegistryImage,
	)
	ginkgo.DeferCleanup(
		func(c ginkgo.SpecContext) {
			_, _ = imageConfigCommand(c, nil, testDockerCommand, "rm", "-f", x.registry)
		},
	)
	x.authority = imageConfigMust(ctx, nil, testDockerCommand, "port", x.registry, "5000/tcp")
	x.authority = "localhost:" + strings.TrimPrefix(x.authority, "127.0.0.1:")
	x.configureNodeRegistry(ctx)
}

func (x *imageConfigFixture) configureNodeRegistry(ctx context.Context) {
	root := x.root
	hostDir := "/etc/containerd/certs.d/" + x.authority
	_, existingErr := imageConfigCommand(
		ctx,
		nil,
		testDockerCommand,
		"exec",
		x.node,
		"test",
		"-e",
		hostDir,
	)
	gomega.Expect(existingErr).
		To(gomega.HaveOccurred(), "fixture must not overwrite an existing containerd host configuration")
	imageConfigMust(ctx, nil, testDockerCommand, "exec", x.node, "mkdir", "-p", hostDir)
	ginkgo.DeferCleanup(func(c ginkgo.SpecContext) {
		_, _ = imageConfigCommand(c, nil, testDockerCommand, "exec", x.node, "rm", "-rf", hostDir)
	})
	hosts := filepath.Join(root, "hosts.toml")
	framework.ExpectNoError(
		os.WriteFile(
			hosts,
			[]byte(
				fmt.Sprintf(
					"server = \"http://%s:5000\"\n[host.\"http://%s:5000\"]\n  capabilities = [\"pull\", \"resolve\"]\n",
					x.registry,
					x.registry,
				),
			),
			0o600,
		),
	)
	imageConfigMust(ctx, nil, testDockerCommand, "cp", hosts, x.node+":"+hostDir+"/hosts.toml")
}

func (x *imageConfigFixture) publishImage(ctx context.Context, arch string) {
	root, password := x.root, x.password
	img, err := image.GetImageForArch(ctx, "ghcr.io/devsy-org/test-images/base:alpine", arch)
	framework.ExpectNoError(err)
	cfg, err := img.ConfigFile()
	framework.ExpectNoError(err)
	cfg.Config.User = "nobody"
	cfg.Config.Entrypoint = []string{
		"/bin/sh",
		"-c",
		"echo " + imageConfigLogMarker + "; echo entrypoint > /tmp/image-config-entrypoint; exec \"$@\"",
		"--",
	}
	cfg.Config.Cmd = []string{"sleep", "36000"}
	cfg.Config.Env = append(cfg.Config.Env, "IMAGE_CONFIG_VALUE=a=b", "IMAGE_CONFIG_OVERRIDE=image")
	cfg.Config.Labels = map[string]string{
		"devsy-e2e-nonce": x.id,
		"devcontainer.metadata": `[{"containerEnv":{"IMAGE_CONFIG_METADATA":"metadata"},` +
			`"postCreateCommand":"echo metadata > /tmp/image-config-lifecycle"}]`,
	}
	img, err = mutate.ConfigFile(img, cfg)
	framework.ExpectNoError(err)
	configDigest, err := img.ConfigName()
	framework.ExpectNoError(err)
	x.configDigest = configDigest.String()
	digest, err := img.Digest()
	framework.ExpectNoError(err)
	x.image = x.authority + "/fixture/workspace@" + digest.String()
	tag, err := name.NewTag(x.authority+"/fixture/workspace:fixture", name.Insecure)
	framework.ExpectNoError(err)
	gomega.Eventually(func() error {
		return remote.Write(
			tag,
			img,
			remote.WithContext(ctx),
			remote.WithAuth(&authn.Basic{Username: "fixture", Password: password}),
		)
	}, 30*time.Second, time.Second).Should(gomega.Succeed())
	x.file = filepath.Join(root, "image.json")
	imageConfigWrite(
		x.file,
		map[string]any{
			"schemaVersion":           1,
			compatibilityImageCommand: x.image,
			"os":                      "linux",
			"architecture":            arch,
			"config": map[string]any{
				"User":       cfg.Config.User,
				"Env":        cfg.Config.Env,
				"Labels":     cfg.Config.Labels,
				"Entrypoint": cfg.Config.Entrypoint,
				"Cmd":        cfg.Config.Cmd,
			},
		},
	)
}

func (x *imageConfigFixture) provisionIdentity(ctx context.Context) {
	password := x.password
	x.kubectl(ctx, "create", "namespace", x.namespace)
	ginkgo.DeferCleanup(func(c ginkgo.SpecContext) {
		_, _ = imageConfigCommand(
			c,
			append(os.Environ(), "KUBECONFIG="+x.admin),
			imageConfigKubectl,
			"delete",
			"namespace",
			x.namespace,
			"--ignore-not-found",
			"--wait=false",
		)
	})
	x.kubectl(
		ctx,
		"-n",
		x.namespace,
		"create",
		"secret",
		"docker-registry",
		"registry-admin",
		"--docker-server="+x.authority,
		"--docker-username=fixture",
		"--docker-password="+password,
	)
	x.kubectl(ctx, "-n", x.namespace, "create", "serviceaccount", cmdWorkspace)
	x.kubectl(
		ctx,
		"-n",
		x.namespace,
		"patch",
		"serviceaccount",
		cmdWorkspace,
		"--type=merge",
		"-p",
		`{"imagePullSecrets":[{"name":"registry-admin"}]}`,
	)
	x.kubectl(ctx, "-n", x.namespace, "create", "serviceaccount", "client")
	x.createClientRole(ctx)
}

func (x *imageConfigFixture) createClientRole(ctx context.Context) {
	root := x.root
	role := fmt.Sprintf(`apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: client, namespace: %s}
rules:
- apiGroups: [""]
  resources: [pods, pods/exec, pods/log, persistentvolumeclaims, serviceaccounts, events, services, configmaps]
  verbs: ["*"]
- apiGroups: [""]
  resources: [secrets]
  verbs: [create]
- apiGroups: [""]
  resources: [secrets]
  resourceNames: [%q, %q]
  verbs: [get, update, patch, delete]
`, x.namespace, imageConfigDaemonSecretPrefix+x.uid, imageConfigDaemonSecretPrefix+x.controlUID)
	rolePath := filepath.Join(root, "role.yaml")
	framework.ExpectNoError(os.WriteFile(rolePath, []byte(role), 0o600))
	x.kubectl(ctx, "apply", "-f", rolePath)
	x.kubectl(
		ctx,
		"-n",
		x.namespace,
		"create",
		"rolebinding",
		"client",
		"--role=client",
		"--serviceaccount="+x.namespace+":client",
	)
}

func (x *imageConfigFixture) configureClient(ctx context.Context, clusterContext string) {
	root := x.root
	token := x.kubectl(ctx, "-n", x.namespace, "create", "token", "client", "--duration=1h")
	kube := filepath.Join(root, "client.kubeconfig")
	raw := x.kubectl(ctx, "config", "view", "--raw", "--minify", "--flatten")
	framework.ExpectNoError(os.WriteFile(kube, []byte(raw), 0o600))
	env := append(os.Environ(), "KUBECONFIG="+kube)
	imageConfigMust(
		ctx,
		env,
		imageConfigKubectl,
		"config",
		"set-credentials",
		"image-config-client",
		"--token="+token,
	)
	imageConfigMust(
		ctx,
		env,
		imageConfigKubectl,
		"config",
		"set-context",
		clusterContext,
		"--user=image-config-client",
		"--namespace="+x.namespace,
	)
	framework.SetEnvForSpec("KUBECONFIG", kube)
	x.configureCredentialSpy()
	x.checkClientPermissions(ctx)
}

func (x *imageConfigFixture) configureCredentialSpy() {
	root := x.root
	framework.SetEnvForSpec("DOCKER_CONFIG", filepath.Join(root, "empty-docker"))
	helperDir := filepath.Join(root, "helpers")
	framework.ExpectNoError(os.MkdirAll(helperDir, 0o700))
	framework.SetEnvForSpec("IMAGE_CONFIG_HELPER_LOG", filepath.Join(root, "helper-calls"))
	for _, helper := range []string{
		"docker-credential-desktop", "docker-credential-osxkeychain",
		"docker-credential-pass", "docker-credential-secretservice",
	} {
		script := []byte("#!/bin/sh\necho called >> \"$IMAGE_CONFIG_HELPER_LOG\"\nexit 1\n")
		framework.ExpectNoError(
			(runenv.OsFileStore{}).WriteFile(filepath.Join(helperDir, helper), script, 0o700),
		)
	}
	framework.SetEnvForSpec("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	framework.ExpectNoError(os.MkdirAll(filepath.Join(root, "empty-docker"), 0o700))
	framework.SetEnvForSpec("DEVSY_HOME", filepath.Join(root, "devsy"))
	framework.SetEnvForSpec("DEVSY_SECRETS_BACKEND", "file")
	framework.SetEnvForSpec("DEVSY_SECRETS_PASSPHRASE", "fixture")
}

func (x *imageConfigFixture) checkClientPermissions(ctx context.Context) {
	out, err := imageConfigCommand(
		ctx,
		nil,
		imageConfigKubectl,
		"auth",
		"can-i",
		"get",
		"secret/registry-admin",
		"-n",
		x.namespace,
	)
	gomega.Expect(err).To(gomega.HaveOccurred())
	gomega.Expect(out).To(gomega.ContainSubstring("no"))
	out, err = imageConfigCommand(ctx, nil, imageConfigKubectl, "auth", "can-i", "list", "nodes")
	gomega.Expect(err).To(gomega.HaveOccurred())
	gomega.Expect(out).To(gomega.ContainSubstring("no"))
	out, err = imageConfigCommand(ctx, nil, imageConfigKubectl, "auth", "can-i", "get",
		"secret/"+imageConfigDaemonSecretPrefix+x.uid, "-n", x.namespace)
	framework.ExpectNoError(err)
	gomega.Expect(out).To(gomega.Equal("yes"))
}

func (x *imageConfigFixture) configureProvider(ctx context.Context, arch string) {
	root := x.root
	_, stderr, err := x.f.ExecCommandCapture(
		ctx,
		[]string{
			"context",
			microsandboxSecretSetCommand,
			microsandboxResourceOption,
			"SSH_INJECT_DOCKER_CREDENTIALS=false",
			microsandboxResourceOption,
			"SSH_ADD_PRIVATE_KEYS=false",
		},
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), stderr)
	framework.ExpectNoError(
		x.f.DevsyProviderAdd(
			ctx,
			"kubernetes",
			"--name",
			x.id,
			"-o",
			"KUBERNETES_NAMESPACE="+x.namespace,
			"-o",
			"CREATE_NAMESPACE=false",
			"-o",
			"SERVICE_ACCOUNT=workspace",
			"-o",
			"ARCHITECTURE="+arch,
			"-o",
			"KUBERNETES_PULL_SECRETS_ENABLED=false",
			"-o",
			"POD_TIMEOUT=2m",
			"-o",
			"INACTIVITY_TIMEOUT=0",
		),
	)
	ginkgo.DeferCleanup(
		func(c ginkgo.SpecContext) { _ = x.f.CleanupWorkspace(c, x.id); _ = x.f.DevsyProviderDelete(c, x.id) },
	)
	x.dir = filepath.Join(root, cmdWorkspace)
	framework.ExpectNoError(os.MkdirAll(filepath.Join(x.dir, ".devcontainer"), 0o700))
	imageConfigWrite(
		filepath.Join(x.dir, ".devcontainer", "devcontainer.json"),
		map[string]any{
			compatibilityImageCommand: x.image,
			"workspaceFolder":         "/workspaces/project",
			"workspaceMount":          "source=${localWorkspaceFolder},target=/workspaces/project,type=bind",
			"overrideCommand":         false,
			imageConfigContainerUser:  microsandboxRootUser,
			imageConfigRemoteUser:     microsandboxRootUser,
			"containerEnv":            map[string]string{"IMAGE_CONFIG_OVERRIDE": "override"},
		},
	)
}

func (x *imageConfigFixture) up(ctx context.Context, args ...string) (string, string, error) {
	command := []string{
		cmdWorkspace,
		"up",
		x.dir,
		"--provider",
		x.id,
		"--id",
		x.id,
		imageConfigUIDFlag,
		x.uid,
		flagIDEOption,
		kubernetesIgnoreIDE,
		"--provider-option",
		"ARCHITECTURE=" + runtime.GOARCH,
		"--provider-option",
		"POD_TIMEOUT=2m",
	}
	return x.f.ExecCommandCapture(ctx, append(command, args...))
}

func (x *imageConfigFixture) pod(ctx context.Context) corev1.Pod {
	raw := x.kubectl(
		ctx,
		"-n",
		x.namespace,
		"get",
		"pods",
		"-l",
		"devsy.sh/created=true",
		"-o",
		"json",
	)
	var list corev1.PodList
	framework.ExpectNoError(json.Unmarshal([]byte(raw), &list))
	gomega.Expect(list.Items).To(gomega.HaveLen(1))
	return list.Items[0]
}

var _ = ginkgo.Describe(
	"Kubernetes supplied image config",
	ginkgo.Label("up-provider-kubernetes", "up-provider-kubernetes-image-config-file"),
	func() {
		ginkgo.It(
			"uses cluster registry authorization without client inspection and preserves workspace data",
			func(ctx ginkgo.SpecContext) {
				x := newImageConfigFixture(ctx)
				negativeStarted := time.Now().UTC().Format(time.RFC3339Nano)
				_, stderr, err := x.up(ctx)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(strings.ToLower(stderr)).
					To(gomega.Or(gomega.ContainSubstring("unauthorized"), gomega.ContainSubstring("401")))
				negativeLogs := imageConfigMust(ctx, nil, testDockerCommand,
					"logs", "--since", negativeStarted, x.registry)
				gomega.Expect(negativeLogs).To(gomega.ContainSubstring(" 401 "))
				gomega.Expect(negativeLogs).To(gomega.ContainSubstring("/manifests/"))
				_, stderr, err = x.up(
					ctx,
					"--provider-option",
					"IMAGE_CONFIG_FILE="+filepath.Join(x.root, "missing.json"),
				)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(stderr).To(gomega.ContainSubstring("IMAGE_CONFIG_FILE"))
				_, stderr, err = x.up(
					ctx,
					"--provider-option",
					"IMAGE_CONFIG_FILE="+x.file,
					"--provider-option",
					"ARCHITECTURE=wrong",
				)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(stderr).To(gomega.ContainSubstring("ARCHITECTURE"))
				pods := x.kubectl(
					ctx,
					"-n",
					x.namespace,
					"get",
					"pods",
					"-l",
					"devsy.sh/created=true",
					"-o",
					"json",
				)
				var empty corev1.PodList
				framework.ExpectNoError(json.Unmarshal([]byte(pods), &empty))
				gomega.Expect(empty.Items).To(gomega.BeEmpty())
				valid, err := os.ReadFile(x.file)
				framework.ExpectNoError(err)
				var mismatch map[string]any
				framework.ExpectNoError(json.Unmarshal(valid, &mismatch))
				mismatch[compatibilityImageCommand] = x.authority + "/fixture/workspace@sha256:" + strings.Repeat(
					"0",
					64,
				)
				imageConfigWrite(x.file, mismatch)
				_, mismatchError, err := x.up(ctx, "--provider-option", "IMAGE_CONFIG_FILE="+x.file)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(mismatchError).To(gomega.ContainSubstring("IMAGE_CONFIG_FILE"))
				gomega.Expect(x.kubectl(
					ctx,
					"-n",
					x.namespace,
					"get",
					"pods",
					"-l",
					"devsy.sh/created=true",
					"-o",
					"jsonpath={.items[*].metadata.name}",
				)).To(gomega.BeEmpty())
				framework.ExpectNoError(os.WriteFile(x.file, valid, 0o600))
				x.kubectl(
					ctx,
					"-n",
					x.namespace,
					"patch",
					"serviceaccount",
					cmdWorkspace,
					"--type=merge",
					"-p",
					`{"imagePullSecrets":[]}`,
				)
				_, pullError, err := x.up(
					ctx,
					"--provider-option",
					"IMAGE_CONFIG_FILE="+x.file,
					"--provider-option",
					"POD_TIMEOUT=30s",
				)
				gomega.Expect(err).To(gomega.HaveOccurred())
				events := x.kubectl(ctx, "-n", x.namespace, "get", "events", "-o", "json")
				gomega.Expect(strings.ToLower(pullError + events)).
					To(gomega.Or(gomega.ContainSubstring("imagepullbackoff"), gomega.ContainSubstring("pull image")))
				framework.ExpectNoError(x.f.CleanupWorkspace(ctx, x.id))
				x.kubectl(
					ctx,
					"-n",
					x.namespace,
					"patch",
					"serviceaccount",
					cmdWorkspace,
					"--type=merge",
					"-p",
					`{"imagePullSecrets":[{"name":"registry-admin"}]}`,
				)
				helperLog := filepath.Join(x.root, "helper-calls")
				if err := os.Remove(helperLog); err != nil && !os.IsNotExist(err) {
					framework.ExpectNoError(err)
				}
				launchStarted := time.Now().UTC().Format(time.RFC3339Nano)
				_, stderr, err = x.up(ctx, "--provider-option", "IMAGE_CONFIG_FILE="+x.file)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), stderr)
				pod := x.pod(ctx)
				gomega.Expect(pod.Spec.ServiceAccountName).To(gomega.Equal(cmdWorkspace))
				gomega.Expect(pod.Spec.Containers[0].Image).To(gomega.Equal(x.image))
				gomega.Expect(pod.Status.ContainerStatuses[0].ImageID).
					To(gomega.ContainSubstring(strings.Split(x.image, "@")[1]))
				delta := imageConfigMust(
					ctx,
					nil,
					testDockerCommand,
					"logs",
					"--since",
					launchStarted,
					x.registry,
				)
				gomega.Expect(strings.ToLower(delta)).To(gomega.ContainSubstring("containerd"))
				nodeIP := imageConfigMust(
					ctx,
					nil,
					testDockerCommand,
					"inspect",
					"-f",
					"{{(index .NetworkSettings.Networks \"kind\").IPAddress}}",
					x.node,
				)
				nodeRequests := 0
				configFetched := false
				for line := range strings.SplitSeq(delta, "\n") {
					if strings.Contains(line, "http.request.remoteaddr") {
						gomega.Expect(line).To(gomega.ContainSubstring(nodeIP))
						nodeRequests++
						if strings.Contains(line, "http.request.method=GET") &&
							strings.Contains(line, "/blobs/"+x.configDigest) &&
							strings.Contains(line, "http.response.status=200") {
							configFetched = true
						}
					}
				}
				gomega.Expect(nodeRequests).To(gomega.BeNumerically(">", 0))
				gomega.Expect(configFetched).
					To(gomega.BeTrue(), "node must fetch the unique image config")
				_, err = os.Stat(helperLog)
				gomega.Expect(err).To(gomega.MatchError(os.ErrNotExist))
				observe := "printf '%s|%s|%s|%s' \"$(id -un)\" \"$IMAGE_CONFIG_VALUE\" " +
					"\"$IMAGE_CONFIG_OVERRIDE\" \"$IMAGE_CONFIG_METADATA\" && " +
					"test -f /tmp/image-config-lifecycle && test -f /tmp/image-config-entrypoint && " +
					"tr '\\0' ' ' </proc/1/cmdline"
				output, err := x.f.DevsySSH(ctx, x.id, observe)
				framework.ExpectNoError(err)
				gomega.Expect(output).To(gomega.ContainSubstring("root|a=b|override|metadata"))
				gomega.Expect(output).To(gomega.ContainSubstring("sleep 36000"))
				_, err = x.f.DevsySSH(ctx, x.id, "touch /workspaces/project/image-config-sentinel")
				framework.ExpectNoError(err)
				uid := pod.UID
				pvc := x.kubectl(
					ctx,
					"-n",
					x.namespace,
					"get",
					"pvc",
					"-o",
					"jsonpath={.items[0].metadata.uid}",
				)
				moved := filepath.Join(x.root, "moved-image.json")
				framework.ExpectNoError(os.WriteFile(moved, valid, 0o600))
				_, stderr, err = x.up(ctx, "--provider-option", "IMAGE_CONFIG_FILE="+moved)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), stderr)
				movedPod := x.pod(ctx)
				gomega.Expect(movedPod.UID).To(gomega.Equal(uid))
				annotation := movedPod.Annotations[kubernetes.DevsyLastAppliedAnnotation]
				gomega.Expect(annotation).NotTo(gomega.BeEmpty())
				gomega.Expect(annotation).NotTo(gomega.ContainSubstring(x.file))
				gomega.Expect(annotation).NotTo(gomega.ContainSubstring(moved))
				framework.ExpectNoError(os.Remove(x.file))
				x.file = moved
				framework.ExpectNoError(os.WriteFile(x.file, []byte(`{"schemaVersion":0}`), 0o600))
				_, _, err = x.up(
					ctx,
					"--recreate",
					"--provider-option",
					"IMAGE_CONFIG_FILE="+x.file,
				)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(x.pod(ctx).UID).To(gomega.Equal(uid))
				gomega.Expect(x.kubectl(ctx, "-n", x.namespace, "get", "pvc", "-o", "jsonpath={.items[0].metadata.uid}")).
					To(gomega.Equal(pvc))
				framework.ExpectNoError(os.Remove(x.file))
				_, err = x.f.DevsyStatus(ctx, x.id)
				framework.ExpectNoError(err)
				x.observeLogs(ctx)
				framework.ExpectNoError(x.f.DevsyWorkspaceStop(ctx, x.id))
				framework.ExpectNoError(os.WriteFile(x.file, valid, 0o600))
				_, stderr, err = x.up(ctx, "--provider-option", "IMAGE_CONFIG_FILE="+x.file)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), stderr)
				_, err = x.f.DevsySSH(
					ctx,
					x.id,
					"test -f /workspaces/project/image-config-sentinel",
				)
				framework.ExpectNoError(err)
				stoppedPod := x.pod(ctx)
				_, stderr, err = x.up(
					ctx,
					"--recreate",
					"--provider-option",
					"IMAGE_CONFIG_FILE="+x.file,
				)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), stderr)
				gomega.Expect(x.pod(ctx).UID).NotTo(gomega.Equal(stoppedPod.UID))
				gomega.Expect(x.kubectl(ctx, "-n", x.namespace, "get", "pvc", "-o", "jsonpath={.items[0].metadata.uid}")).
					To(gomega.Equal(pvc))
				_, err = x.f.DevsySSH(
					ctx,
					x.id,
					"test -f /workspaces/project/image-config-sentinel",
				)
				framework.ExpectNoError(err)

				authDir := filepath.Join(x.root, "control-docker")
				framework.ExpectNoError(os.MkdirAll(authDir, 0o700))
				imageConfigWrite(
					filepath.Join(authDir, "config.json"),
					map[string]any{
						"auths": map[string]any{
							x.authority: map[string]string{
								"auth": base64.StdEncoding.EncodeToString(
									[]byte("fixture:" + x.password),
								),
							},
						},
					},
				)
				framework.ExpectNoError(os.Setenv("DOCKER_CONFIG", authDir))
				controlID := x.id + "control"
				ginkgo.DeferCleanup(
					func(c ginkgo.SpecContext) { _ = x.f.CleanupWorkspace(c, controlID) },
				)
				_, stderr, err = x.f.ExecCommandCapture(
					ctx,
					[]string{
						cmdWorkspace,
						"up",
						x.dir,
						"--provider",
						x.id,
						"--id",
						controlID,
						imageConfigUIDFlag,
						x.controlUID,
						flagIDEOption,
						kubernetesIgnoreIDE,
					},
				)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), stderr)
				control, err := x.f.DevsySSH(ctx, controlID, observe)
				framework.ExpectNoError(err)
				gomega.Expect(control).To(gomega.Equal(output))
				framework.ExpectNoError(
					os.Setenv("DOCKER_CONFIG", filepath.Join(x.root, "empty-docker")),
				)
				framework.ExpectNoError(os.Remove(x.file))
				framework.ExpectNoError(x.f.DevsyWorkspaceDelete(ctx, x.id))
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)
	},
)
