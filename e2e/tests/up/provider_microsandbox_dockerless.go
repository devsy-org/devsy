package up

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	devsyconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/envfile"
	"github.com/devsy-org/devsy/pkg/flags/names"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	microsandboxDockerlessFixture = "tests/up/testdata/microsandbox-dockerless"
	microsandboxDockerlessRoot    = "/opt/devsy-dockerless-parity"
	microsandboxDockerlessPath    = microsandboxDockerlessRoot +
		"/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	microsandboxDockerlessCommand    = "devsy-dockerless-parity-check"
	microsandboxDockerlessUser       = "dlparity"
	microsandboxDockerlessOff        = "off"
	microsandboxDockerlessDockerfile = "Dockerfile"
	microsandboxDockerlessSentinel   = "dockerless-host-sentinel"
	microsandboxDockerlessRootMarker = "/root/dockerless-lifecycle-marker"
)

type microsandboxDockerlessCase struct {
	user    string
	policy  string
	refused bool
}

type microsandboxDockerlessFixtureState struct {
	f              *framework.Framework
	workspace      string
	uid            string
	label          string
	provider       string
	workspaceOwner string
	sentinelOwner  string
	sentinelMode   os.FileMode
	configBefore   []byte
	dockerLog      string
}

var _ = ginkgo.Describe("microsandbox Dockerless parity",
	ginkgo.Label("up-provider-microsandbox-dockerless"), func() {
		for _, provider := range microsandboxProviders("builtin-dockerless", "external-dockerless") {
			ginkgo.Context(provider.name, ginkgo.Label(provider.label), func() {
				ginkgo.DescribeTable(
					"runs without host Docker and enforces developer ownership",
					func(ctx context.Context, testCase microsandboxDockerlessCase) {
						fixture := newMicrosandboxDockerlessFixture(
							ctx,
							provider.name,
							provider.source,
						)
						fixture.configure(ctx, testCase)
						if testCase.refused {
							fixture.assertRefused(ctx)
							return
						}
						fixture.up(ctx, true)
						fixture.verify(ctx, testCase.user)
						if testCase.user == microsandboxRootUser {
							fixture.lifecycle(ctx)
						} else {
							fixture.verifyNonrootWrite(ctx)
						}
					},
					ginkgo.Entry(
						"root Dockerfile and Feature retain image environment through lifecycle",
						microsandboxDockerlessCase{user: microsandboxRootUser, policy: "strict"},
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry(
						"nonroot off/private runs with literal host permissions",
						microsandboxDockerlessCase{
							user:   microsandboxDockerlessUser,
							policy: microsandboxDockerlessOff,
						},
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry(
						"nonroot strict rejects fresh creation before VM or volume mutation",
						microsandboxDockerlessCase{
							user:    microsandboxDockerlessUser,
							policy:  "strict",
							refused: true,
						},
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry(
						"nonroot relaxed rejects fresh creation before VM or volume mutation",
						microsandboxDockerlessCase{
							user:    microsandboxDockerlessUser,
							policy:  "relaxed",
							refused: true,
						},
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
				)
			})
		}
	})

func newMicrosandboxDockerlessFixture(
	ctx context.Context, providerName, providerSource string,
) *microsandboxDockerlessFixtureState {
	framework.SkipUnlessOS(
		"linux",
		"Dockerless parity fixture currently targets the supported Linux KVM job",
	)
	skipIfNoMicrosandbox(ctx)
	ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
	ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
	initialDir, err := os.Getwd()
	framework.ExpectNoError(err)
	workspace, err := framework.CopyToTempDir(microsandboxDockerlessFixture)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, workspace)
	fixture := &microsandboxDockerlessFixtureState{
		f:         framework.NewDefaultFramework(filepath.Join(initialDir, "bin")),
		workspace: workspace,
		uid:       encoding.CreateNewUID("", filepath.Base(workspace)),
		provider:  providerName,
	}
	fixture.label = "devsy.dockerless-parity=" + fixture.uid
	assertMicrosandboxEphemeralAbsent(ctx, fixture.label)
	fixture.assertSandboxAbsent(ctx)
	gomega.Expect(fixture.volumeExists(ctx)).To(gomega.BeFalse(), "never adopt an existing volume")
	framework.ExpectNoError(fixture.f.DevsyProviderAdd(ctx, providerSource, "--name", providerName))
	fixture.registerCleanup()
	fixture.dockerLog = denyMicrosandboxHostDocker(ctx)
	return fixture
}

func (fixture *microsandboxDockerlessFixtureState) registerCleanup() {
	ginkgo.DeferCleanup(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return fixture.f.DevsyProviderDelete(ctx, fixture.provider)
	})
	ginkgo.DeferCleanup(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		volumes, err := microsandboxDockerlessVolumes(ctx)
		if err != nil || !volumes[fixture.volume()] {
			return err
		}
		out, err := microsandboxResourceCommand(ctx, "volume", "rm", fixture.volume())
		if err != nil {
			return fmt.Errorf("remove owned Dockerless volume: %w: %s", err, out)
		}
		return nil
	})
	ginkgo.DeferCleanup(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		out, err := microsandboxResourceCommand(ctx, "list", "--quiet")
		if err != nil {
			return err
		}
		for name := range strings.FieldsSeq(string(out)) {
			if name != "devsy-"+fixture.uid {
				continue
			}
			out, err = microsandboxResourceCommand(ctx, "remove", "--force", name)
			if err != nil {
				return fmt.Errorf("remove owned Dockerless sandbox: %w: %s", err, out)
			}
		}
		return nil
	})
	ginkgo.DeferCleanup(fixture.f.CleanupWorkspace, fixture.workspace)
}

func (fixture *microsandboxDockerlessFixtureState) configure(
	ctx context.Context, testCase microsandboxDockerlessCase,
) {
	framework.ExpectNoError(fixture.f.DevsyProviderUse(ctx, fixture.provider,
		microsandboxResourceOption, "MICROSANDBOX_WORKSPACE_STAT_VIRTUALIZATION="+testCase.policy,
		microsandboxResourceOption, "MICROSANDBOX_WORKSPACE_HOST_PERMISSIONS=private"))
	cfg := struct {
		Build               config.ConfigBuildOptions `json:"build"`
		Features            map[string]any            `json:"features"`
		ContainerUser       string                    `json:"containerUser"`
		RemoteUser          string                    `json:"remoteUser"`
		UpdateRemoteUserUID bool                      `json:"updateRemoteUserUID"`
		UserEnvProbe        config.UserEnvProbe       `json:"userEnvProbe"`
		RemoteEnv           map[string]string         `json:"remoteEnv"`
		WaitFor             string                    `json:"waitFor"`
		PostStartCommand    string                    `json:"postStartCommand"`
	}{
		Build: config.ConfigBuildOptions{
			Dockerfile: microsandboxDockerlessDockerfile,
			Context:    ".",
		},
		Features:         map[string]any{"./features/parity": map[string]any{}},
		ContainerUser:    testCase.user,
		RemoteUser:       testCase.user,
		UserEnvProbe:     config.NoneProbe,
		RemoteEnv:        map[string]string{"DOCKER_CONFIG": "${containerEnv:DOCKER_CONFIG}"},
		WaitFor:          "postStartCommand",
		PostStartCommand: microsandboxDockerlessCommand + " > /tmp/devsy-dockerless-post-start.env",
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	framework.ExpectNoError(err)
	fixture.configBefore = data
	framework.ExpectNoError(
		os.WriteFile(filepath.Join(fixture.workspace, ".devcontainer.json"), data, 0o600),
	)
	file, err := os.OpenFile(
		filepath.Join(fixture.workspace, microsandboxDockerlessDockerfile),
		os.O_APPEND|os.O_WRONLY,
		0,
	)
	framework.ExpectNoError(err)
	_, writeErr := fmt.Fprintf(file, "USER %s\n", testCase.user)
	closeErr := file.Close()
	framework.ExpectNoError(writeErr)
	framework.ExpectNoError(closeErr)
	// Off/private must be usable by a guest UID different from the host owner.
	// These permissions apply only to this temporary fixture, never user paths.
	// #nosec G302 -- Only the owned temporary source must be writable by the distinct guest UID.
	framework.ExpectNoError(os.Chmod(fixture.workspace, 0o777))
	fixture.workspaceOwner = microsandboxHostOwner(ctx, fixture.workspace)
	sentinel := filepath.Join(fixture.workspace, microsandboxDockerlessSentinel)
	framework.ExpectNoError(os.WriteFile(sentinel, []byte("host-preserved\n"), 0o600))
	// #nosec G302 -- The owned temporary sentinel must be readable by the distinct guest UID.
	framework.ExpectNoError(os.Chmod(sentinel, 0o644))
	fixture.sentinelOwner = microsandboxHostOwner(ctx, sentinel)
	info, err := os.Stat(sentinel)
	framework.ExpectNoError(err)
	fixture.sentinelMode = info.Mode().Perm()
	if testCase.user != microsandboxRootUser && testCase.policy == microsandboxDockerlessOff {
		for _, owner := range []string{fixture.workspaceOwner, fixture.sentinelOwner} {
			gomega.Expect(strings.SplitN(owner, ":", 2)[0]).NotTo(gomega.Equal("31001"),
				"off-policy proof requires a host owner distinct from the guest UID")
		}
	}
}

func denyMicrosandboxHostDocker(ctx context.Context) string {
	dir := ginkgo.GinkgoT().TempDir()
	logPath := filepath.Join(dir, "docker-attempts")
	framework.ExpectNoError(os.WriteFile(logPath, nil, 0o600))
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + microsandboxDockerlessQuote(logPath) +
		"\nprintf '%s\\n' 'Docker unavailable in Dockerless parity fixture' >&2\nexit 127\n"
	// #nosec G306 -- This owned executable fixture denies every host Docker invocation.
	framework.ExpectNoError(os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700))
	ginkgo.GinkgoT().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ginkgo.GinkgoT().Setenv("DOCKER_HOST", "unix://"+filepath.Join(dir, "absent-docker.sock"))
	ginkgo.GinkgoT().Setenv("DOCKER_CONTEXT", "")
	ginkgo.GinkgoT().Setenv("DOCKER_API_VERSION", "")
	ginkgo.GinkgoT().Setenv("DOCKER_TLS_VERIFY", "")
	ginkgo.GinkgoT().Setenv("DOCKER_CERT_PATH", "")
	ginkgo.GinkgoT().Setenv("DOCKER_CONFIG", ginkgo.GinkgoT().TempDir())
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(probeCtx, "docker", "version")
	command.WaitDelay = 2 * time.Second
	out, err := command.CombinedOutput()
	gomega.Expect(err).To(gomega.HaveOccurred())
	gomega.Expect(string(out)).
		To(gomega.ContainSubstring("Docker unavailable in Dockerless parity fixture"))
	framework.ExpectNoError(probeCtx.Err())
	assertMicrosandboxDockerAPIDenied(ctx)
	framework.ExpectNoError(os.WriteFile(logPath, nil, 0o600))
	return logPath
}

func assertMicrosandboxDockerAPIDenied(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := docker.NewClient(probeCtx)
	framework.ExpectNoError(err)
	defer func() { framework.ExpectNoError(client.Close()) }()
	gomega.Expect(client.DaemonHost()).To(gomega.Equal(os.Getenv("DOCKER_HOST")))
	_, err = client.Ping(probeCtx)
	gomega.Expect(err).
		To(gomega.HaveOccurred(), "the owned absent Docker socket must be unreachable")
	framework.ExpectNoError(probeCtx.Err())
	gomega.Expect(errors.Is(err, context.Canceled)).To(gomega.BeFalse())
	gomega.Expect(errors.Is(err, context.DeadlineExceeded)).To(gomega.BeFalse())
}

func (fixture *microsandboxDockerlessFixtureState) up(
	ctx context.Context,
	mustBuild bool,
	extra ...string,
) {
	stdout, stderr, err := fixture.upOutput(ctx, extra...)
	attempts, readErr := os.ReadFile(fixture.dockerLog)
	framework.ExpectNoError(readErr)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(),
		"single up attempt: stdout=%s stderr=%s denied Docker calls=%s", stdout, stderr, attempts)
	output := stdout + stderr
	gomega.Expect(output).NotTo(gomega.ContainSubstring("failed to configure docker credentials"))
	gomega.Expect(output).NotTo(gomega.ContainSubstring("docker credentials disabled"))
	if mustBuild {
		gomega.Expect(output).To(gomega.ContainSubstring(
			"Wrote docker credentials helper to /.dockerless/.docker/docker-credentials-"))
		gomega.Expect(output).To(gomega.ContainSubstring("starting dockerless build"))
		gomega.Expect(output).To(gomega.ContainSubstring("dockerless build completed"))
	} else {
		gomega.Expect(output).NotTo(gomega.ContainSubstring("starting dockerless build"))
	}
}

func (fixture *microsandboxDockerlessFixtureState) upOutput(
	ctx context.Context, extra ...string,
) (string, string, error) {
	args := []string{
		cmdWorkspace,
		"up",
		names.Flag(names.Debug),
		names.Flag(names.IDE),
		string(devsyconfig.IDENone),
		fixture.workspace,
		"--uid",
		fixture.uid,
		"--id-label",
		fixture.label,
		"--devcontainer",
		".devcontainer.json",
		"--force-dockerless",
	}
	return fixture.f.ExecCommandCapture(ctx, append(args, extra...))
}

func (fixture *microsandboxDockerlessFixtureState) verify(ctx context.Context, user string) {
	identity := "0:0"
	if user != microsandboxRootUser {
		identity = "31001:31001"
	}
	expected := "command=" + microsandboxDockerlessRoot + "/bin/" + microsandboxDockerlessCommand +
		"\nPATH=" + microsandboxDockerlessPath + "\nDOCKER_CONFIG=" + microsandboxDockerlessRoot + "/docker-config" +
		"\nimage=from-image\nfeature=feature-parity\nidentity=" + identity +
		"\ndockerfile-parity\nfeature-parity\n"
	// A bare command and disabled userEnvProbe prevent a login shell from hiding image PATH loss.
	gomega.Expect(fixture.ssh(ctx, microsandboxDockerlessCommand)).To(gomega.Equal(expected))
	gomega.Expect(fixture.ssh(ctx, "cat /tmp/devsy-dockerless-post-start.env")).
		To(gomega.Equal(expected))
	fixture.assertSourcePreserved(ctx)
	gomega.Expect(fixture.volumeExists(ctx)).To(gomega.BeTrue())
	info, err := fixture.f.FindWorkspace(ctx, fixture.workspace)
	framework.ExpectNoError(err)
	gomega.Expect(info.UID).To(gomega.Equal(fixture.uid))
	if user == microsandboxRootUser {
		fixture.verifyImageEnvironment(ctx)
	}
}

func (fixture *microsandboxDockerlessFixtureState) verifyImageEnvironment(ctx context.Context) {
	var image v1.ConfigFile
	framework.ExpectNoError(
		json.Unmarshal([]byte(fixture.ssh(ctx, "cat /.dockerless/image.json")), &image),
	)
	gomega.Expect(image.Config.Env).To(gomega.ContainElement("PATH=" + microsandboxDockerlessPath))
	gomega.Expect(image.Config.Env).
		To(gomega.ContainElement("DOCKER_CONFIG=" + microsandboxDockerlessRoot + "/docker-config"))
	gomega.Expect(image.Config.Env).To(gomega.ContainElement("DEVSY_DOCKERLESS_IMAGE=from-image"))
	var persisted envfile.EnvFile
	framework.ExpectNoError(
		json.Unmarshal([]byte(fixture.ssh(ctx, "cat /etc/envfile.json")), &persisted),
	)
	gomega.Expect(persisted.Env["PATH"]).To(gomega.Equal(microsandboxDockerlessPath))
	gomega.Expect(persisted.Env["DOCKER_CONFIG"]).
		To(gomega.Equal(microsandboxDockerlessRoot + "/docker-config"))
	fixture.ssh(
		ctx,
		"for dir in /.dockerless/.docker/docker-credentials-*; do test ! -e \"$dir\" || exit 1; done",
	)
}

func (fixture *microsandboxDockerlessFixtureState) lifecycle(ctx context.Context) {
	sandbox := "devsy-" + fixture.uid
	createdAt := microsandboxCreationTime(ctx, sandbox)
	bootID := microsandboxStorageBootID(ctx, fixture.f, fixture.workspace)
	fixture.ssh(ctx, "printf 'root-preserved\\n' > "+microsandboxDockerlessRootMarker)
	fixture.up(ctx, false)
	fixture.verify(ctx, microsandboxRootUser)
	gomega.Expect(microsandboxCreationTime(ctx, sandbox)).To(gomega.Equal(createdAt))
	gomega.Expect(microsandboxStorageBootID(ctx, fixture.f, fixture.workspace)).
		To(gomega.Equal(bootID))
	gomega.Expect(fixture.ssh(ctx, "cat "+microsandboxDockerlessRootMarker)).
		To(gomega.Equal("root-preserved\n"))
	framework.ExpectNoError(fixture.f.DevsyWorkspaceStop(ctx, fixture.workspace))
	assertMicrosandboxEphemeralStopped(ctx, sandbox, createdAt)
	fixture.up(ctx, false)
	fixture.verify(ctx, microsandboxRootUser)
	gomega.Expect(microsandboxCreationTime(ctx, sandbox)).To(gomega.Equal(createdAt))
	restartedBootID := microsandboxStorageBootID(ctx, fixture.f, fixture.workspace)
	gomega.Expect(restartedBootID).NotTo(gomega.Equal(bootID))
	gomega.Expect(fixture.ssh(ctx, "cat "+microsandboxDockerlessRootMarker)).
		To(gomega.Equal("root-preserved\n"))
	fixture.up(ctx, true, "--recreate")
	fixture.verify(ctx, microsandboxRootUser)
	gomega.Expect(microsandboxCreationTime(ctx, sandbox)).NotTo(gomega.Equal(createdAt))
	gomega.Expect(microsandboxStorageBootID(ctx, fixture.f, fixture.workspace)).
		NotTo(gomega.Equal(restartedBootID))
	fixture.ssh(ctx, "test ! -e "+microsandboxDockerlessRootMarker)
}

func (fixture *microsandboxDockerlessFixtureState) verifyNonrootWrite(ctx context.Context) {
	path := filepath.Join("/workspaces", filepath.Base(fixture.workspace), "dockerless-guest-write")
	fixture.ssh(ctx, "printf 'guest-write\\n' > "+microsandboxDockerlessQuote(path))
	data, err := os.ReadFile(filepath.Join(fixture.workspace, "dockerless-guest-write"))
	framework.ExpectNoError(err)
	gomega.Expect(string(data)).To(gomega.Equal("guest-write\n"))
	fixture.assertSourcePreserved(ctx)
}

func (fixture *microsandboxDockerlessFixtureState) assertRefused(ctx context.Context) {
	stdout, stderr, err := fixture.upOutput(ctx)
	gomega.Expect(err).To(gomega.HaveOccurred())
	gomega.Expect(stdout + stderr).To(gomega.ContainSubstring(
		"cannot resolve a non-root Dockerless workspace owner before VM creation"))
	gomega.Expect(stdout + stderr).NotTo(gomega.ContainSubstring("starting dockerless build"))
	assertMicrosandboxEphemeralAbsent(ctx, fixture.label)
	fixture.assertSandboxAbsent(ctx)
	gomega.Expect(fixture.volumeExists(ctx)).To(gomega.BeFalse())
	fixture.assertSourcePreserved(ctx)
}

func (fixture *microsandboxDockerlessFixtureState) assertSourcePreserved(ctx context.Context) {
	gomega.Expect(microsandboxHostOwner(ctx, fixture.workspace)).
		To(gomega.Equal(fixture.workspaceOwner))
	dirInfo, err := os.Stat(fixture.workspace)
	framework.ExpectNoError(err)
	gomega.Expect(dirInfo.Mode().Perm()).To(gomega.Equal(os.FileMode(0o777)))
	sentinel := filepath.Join(fixture.workspace, microsandboxDockerlessSentinel)
	// #nosec G304 -- The path is the sentinel in this test-owned temporary workspace.
	data, err := os.ReadFile(sentinel)
	framework.ExpectNoError(err)
	gomega.Expect(string(data)).To(gomega.Equal("host-preserved\n"))
	gomega.Expect(microsandboxHostOwner(ctx, sentinel)).To(gomega.Equal(fixture.sentinelOwner))
	info, err := os.Stat(sentinel)
	framework.ExpectNoError(err)
	gomega.Expect(info.Mode().Perm()).To(gomega.Equal(fixture.sentinelMode))
	data, err = os.ReadFile(filepath.Join(fixture.workspace, ".devcontainer.json"))
	framework.ExpectNoError(err)
	gomega.Expect(data).To(gomega.Equal(fixture.configBefore))
}

func (fixture *microsandboxDockerlessFixtureState) ssh(ctx context.Context, command string) string {
	commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := fixture.f.DevsySSHOnce(commandCtx, fixture.workspace, command)
	framework.ExpectNoError(err, out)
	return out
}

func (fixture *microsandboxDockerlessFixtureState) volume() string {
	return "dockerless-" + fixture.uid
}

func (fixture *microsandboxDockerlessFixtureState) volumeExists(ctx context.Context) bool {
	volumes, err := microsandboxDockerlessVolumes(ctx)
	framework.ExpectNoError(err)
	return volumes[fixture.volume()]
}

func microsandboxDockerlessVolumes(ctx context.Context) (map[string]bool, error) {
	out, err := microsandboxResourceCommand(ctx, "volume", "list", "--quiet")
	if err != nil {
		return nil, err
	}
	volumes := make(map[string]bool)
	for name := range strings.FieldsSeq(string(out)) {
		volumes[name] = true
	}
	return volumes, nil
}

func microsandboxDockerlessQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (fixture *microsandboxDockerlessFixtureState) assertSandboxAbsent(ctx context.Context) {
	out, err := microsandboxResourceCommand(ctx, "list", "--quiet")
	framework.ExpectNoError(err)
	gomega.Expect(strings.Fields(string(out))).NotTo(gomega.ContainElement("devsy-" + fixture.uid))
}
