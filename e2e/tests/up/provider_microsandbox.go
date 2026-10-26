package up

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"testing up command for microsandbox provider",
	ginkgo.Label("up-provider-microsandbox"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func(ctx context.Context) {
			skipIfNoMicrosandbox(ctx)
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.DescribeTable("runs the shared lifecycle and ownership contract",
			func(ctx context.Context, source, name, expectedDriver string) {
				ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
				ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
				f := framework.NewDefaultFramework(initialDir + "/bin")
				tempDir, err := framework.CopyToTempDir("tests/up/testdata/microsandbox")
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
				setMicrosandboxExecRemoteEnv(tempDir)

				err = f.DevsyProviderAdd(ctx, source, "--name", name)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					err := f.DevsyProviderDelete(cleanupCtx, name)
					framework.ExpectNoError(err)
				})
				ginkgo.DeferCleanup(f.CleanupWorkspace, tempDir)

				installed, err := provider.LoadProviderConfig(config.DefaultContext, name)
				framework.ExpectNoError(err)
				gomega.Expect(installed.Agent.Driver).To(gomega.Equal(expectedDriver))
				isBuiltin := expectedDriver == provider.MicrosandboxDriver
				gomega.Expect(installed.Source.Internal).To(gomega.Equal(isBuiltin))
				expectedSource := microsandboxExternalProvider
				if isBuiltin {
					expectedSource = provider.MicrosandboxDriver
				}
				gomega.Expect(installed.Source.Raw).To(gomega.Equal(expectedSource))

				performance, err := newMicrosandboxPerformanceCapture(installed.Source.Raw, name, f)
				framework.ExpectNoError(err)

				// full up: boots the microVM, streams in the agent, opens the tunnel
				err = performance.up(ctx, tempDir, "fresh", "--devcontainer", ".devcontainer.json")
				framework.ExpectNoError(err)

				// the workspace is reachable over SSH
				err = f.DevsySSHEchoTestString(ctx, tempDir)
				framework.ExpectNoError(err)
				assertMicrosandboxSSHStreams(ctx, f, tempDir)
				assertMicrosandboxWorkspaceExec(ctx, f, tempDir)

				workspacePath := filepath.Join("/workspaces", filepath.Base(tempDir))
				_, err = f.DevsySSHOnce(ctx, tempDir, fmt.Sprintf(
					"umask 022; : > %s/guest-created.txt && mkdir %s/guest-created-dir",
					workspacePath, workspacePath,
				))
				framework.ExpectNoError(err)
				for name, mode := range map[string]os.FileMode{"guest-created.txt": 0o644, "guest-created-dir": 0o755} {
					info, err := os.Stat(filepath.Join(tempDir, name))
					framework.ExpectNoError(err)
					gomega.Expect(info.Mode().Perm()).To(gomega.Equal(mode))
				}

				workspace, err := f.FindWorkspace(ctx, tempDir)
				framework.ExpectNoError(err)
				sandbox := "devsy-" + devcontainer.GetRunnerIDFromWorkspace(workspace)
				createdAt := microsandboxCreationTime(ctx, sandbox)
				// No --user override: verify the runtime's default execution identity.
				// #nosec G204 -- fixed command and Devsy-generated sandbox name
				workloadUser, err := exec.CommandContext(
					ctx,
					"msb",
					"exec",
					"--stream",
					sandbox,
					"--",
					"id",
					"-u",
				).Output()
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(string(workloadUser))).To(gomega.Equal("0"))

				developer, err := f.DevsySSHOnce(ctx, tempDir, "id -un")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(developer)).To(gomega.Equal("vscode"))
				assertMicrosandboxHostEntries(ctx, f, tempDir, "fresh")
				err = f.DevsyWorkspaceStop(ctx, tempDir)
				framework.ExpectNoError(err)
				err = performance.up(ctx, tempDir, "reused")
				framework.ExpectNoError(err)
				gomega.Expect(microsandboxCreationTime(ctx, sandbox)).To(gomega.Equal(createdAt))
				assertMicrosandboxHostEntries(ctx, f, tempDir, "restarted")
				framework.ExpectNoError(performance.compare(ctx))
				err = f.DevsyUpRecreate(ctx, tempDir)
				framework.ExpectNoError(err)
				gomega.Expect(microsandboxCreationTime(ctx, sandbox)).NotTo(gomega.Equal(createdAt))
				assertMicrosandboxHostEntries(ctx, f, tempDir, "recreated")

				previousCreation := microsandboxCreationTime(ctx, sandbox)
				// #nosec G204 -- fixed command and Devsy-generated sandbox name
				_, err = exec.CommandContext(
					ctx, "msb", "exec", "--stream", sandbox, "--", "sh", "-c",
					"echo preserved > /root/recreation-preserved.txt",
				).
					Output()
				framework.ExpectNoError(err)

				configPath := filepath.Join(tempDir, ".devcontainer.json")
				// #nosec G304 -- configuration copied into the test-owned temporary directory
				data, err := os.ReadFile(configPath)
				framework.ExpectNoError(err)
				var devConfig map[string]any
				framework.ExpectNoError(json.Unmarshal(data, &devConfig))
				devConfig["remoteUser"] = microsandboxRootUser
				data, err = json.Marshal(devConfig)
				framework.ExpectNoError(err)
				framework.ExpectNoError(os.WriteFile(configPath, data, 0o600))

				stdout, stderr, err := f.DevsyUpStreams(
					ctx, tempDir, "--devcontainer", ".devcontainer.json",
				)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(stdout + stderr).To(gomega.ContainSubstring("--recreate"))
				gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
					To(gomega.Equal(previousCreation))
				// #nosec G204 -- fixed command and Devsy-generated sandbox name
				preserved, err := exec.CommandContext(
					ctx,
					"msb",
					"exec",
					"--stream",
					sandbox,
					"--",
					"cat",
					"/root/recreation-preserved.txt",
				).
					Output()
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(string(preserved))).To(gomega.Equal("preserved"))
				err = f.DevsyUp(ctx, tempDir, "--devcontainer", ".devcontainer.json", "--recreate")
				framework.ExpectNoError(err)
				gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
					NotTo(gomega.Equal(previousCreation))
				developer, err = f.DevsySSHOnce(ctx, tempDir, "id -u")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(developer)).To(gomega.Equal("0"))
				assertMicrosandboxHostEntries(ctx, f, tempDir, "identity-changed")
				framework.ExpectNoError(f.DevsyWorkspaceDelete(ctx, tempDir))
				// #nosec G204 -- fixed command and test-owned sandbox name
				output, err := exec.CommandContext(ctx, "msb", "list", "--format", "json").Output()
				framework.ExpectNoError(err)
				gomega.Expect(string(output)).NotTo(gomega.ContainSubstring(sandbox))
			},
			ginkgo.Entry("built-in", microsandboxResourceBuiltin, "microsandbox-builtin-parity",
				provider.MicrosandboxDriver,
				ginkgo.Label("microsandbox-builtin"), ginkgo.SpecTimeout(framework.TimeoutLong())),
			ginkgo.Entry(
				"external pinned release",
				microsandboxExternalProvider,
				"microsandbox-external-parity",
				provider.ExternalDriver,
				ginkgo.Label("microsandbox-external"), ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
		)
	},
)

func setMicrosandboxExecRemoteEnv(workspace string) {
	configPath := filepath.Join(workspace, ".devcontainer.json")
	// #nosec G304 -- config is copied into this test-owned temporary workspace.
	data, err := os.ReadFile(configPath)
	framework.ExpectNoError(err)
	var devConfig map[string]any
	framework.ExpectNoError(json.Unmarshal(data, &devConfig))
	devConfig["remoteEnv"] = map[string]string{"DEVSY_EXEC_ROUTE": "from-config"}
	data, err = json.Marshal(devConfig)
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(configPath, data, 0o600))
}

func assertMicrosandboxWorkspaceExec(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) {
	const script = `
printf 'cwd=%s\nuser=%s\nenv=%s\narg1=<%s>\narg2=<%s>\narg3=<%s>\nstdin=' \
 "$PWD" "$(id -un)" "$DEVSY_EXEC_ROUTE" "$1" "$2" "$3"
cat
printf '\n'
printf 'exec-stderr-marker\n' >&2
exit 17`
	stdin := []byte("stdin-payload\n")
	stdout, stderr, err := captureWorkspaceExec(
		ctx,
		f,
		workspace,
		microsandboxExecRequest{Stdin: stdin, Args: []string{
			"--remote-env", "DEVSY_EXEC_ROUTE=from-cli", "--",
			"sh", "-c", script, "exec-probe", "space value", "", "semi;$HOME*",
		}},
	)
	var exit *exec.ExitError
	gomega.Expect(errors.As(err, &exit)).To(gomega.BeTrue(),
		"expected workspace exec to return guest exit 17, got %v (stderr %q)", err, stderr)
	gomega.Expect(exit.ExitCode()).To(gomega.Equal(17))
	for _, expected := range []string{
		"cwd=" + filepath.Join("/workspaces", filepath.Base(workspace)),
		"user=vscode",
		"env=from-cli",
		"arg1=<space value>",
		"arg2=<>",
		"arg3=<semi;$HOME*>",
		"stdin=stdin-payload\n",
	} {
		gomega.Expect(stdout).To(gomega.ContainSubstring(expected))
	}
	gomega.Expect(stderr).To(gomega.ContainSubstring("exec-stderr-marker"))
	assertMicrosandboxNumericExecIdentity(ctx, f, workspace)
}

func assertMicrosandboxNumericExecIdentity(
	ctx context.Context,
	f *framework.Framework,
	folder string,
) {
	workspace, err := f.FindWorkspace(ctx, folder)
	framework.ExpectNoError(err)
	result, err := provider.LoadWorkspaceResult(workspace.Context, workspace.ID)
	framework.ExpectNoError(err)
	gomega.Expect(result.MergedConfig).NotTo(gomega.BeNil())
	originalUser := result.MergedConfig.RemoteUser
	defer func() {
		result.MergedConfig.RemoteUser = originalUser
		framework.ExpectNoError(provider.SaveWorkspaceResult(workspace, result))
	}()
	identity, stderr, err := captureWorkspaceExec(ctx, f, folder, microsandboxExecRequest{
		Args: []string{"--", "sh", "-c", "printf '%s:%s' \"$(id -u)\" \"$(id -g)\""},
	})
	framework.ExpectNoError(err, "read fixture identity: %s", stderr)
	uid, gid, found := strings.Cut(identity, ":")
	gomega.Expect(found).To(gomega.BeTrue())
	for user, expected := range map[string]string{
		uid:        uid + ":" + gid,
		uid + ":0": uid + ":0",
	} {
		result.MergedConfig.RemoteUser = user
		framework.ExpectNoError(provider.SaveWorkspaceResult(workspace, result))
		actual, stderr, err := captureWorkspaceExec(ctx, f, folder, microsandboxExecRequest{
			Args: []string{"--", "sh", "-c", "printf '%s:%s' \"$(id -u)\" \"$(id -g)\""},
		})
		framework.ExpectNoError(err, "exec as %s: %s", user, stderr)
		gomega.Expect(actual).To(gomega.Equal(expected))
	}
}

type microsandboxExecRequest struct {
	Stdin []byte
	Args  []string
}

func captureWorkspaceExec(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
	request microsandboxExecRequest,
) (string, string, error) {
	cliArgs := []string{cmdWorkspace, compatibilityExecCommand, workspace}
	cliArgs = append(cliArgs, request.Args...)
	// #nosec G204 -- test binary, test-owned workspace and fixed guest argv
	cmd := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName), cliArgs...)
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = 30 * time.Second
	cmd.Stdin = bytes.NewReader(request.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func assertMicrosandboxSSHStreams(ctx context.Context, f *framework.Framework, workspace string) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	payload := bytes.Repeat([]byte{0, 1, 10, 13, 27, 127, 128, 255}, 128*1024)
	var stdout, stderr bytes.Buffer
	// #nosec G204 -- test binary, test-owned workspace, and fixed guest command
	command := exec.CommandContext(ctx, filepath.Join(f.DevsyBinDir, f.DevsyBinName),
		"workspace", "ssh", workspace, "--command", "cat; printf parity-stderr >&2; exit 23")
	docker.PrepareForGroupCancellation(command)
	command.WaitDelay = 30 * time.Second
	command.Stdin = bytes.NewReader(payload)
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	var exit *exec.ExitError
	gomega.Expect(errors.As(err, &exit)).
		To(gomega.BeTrue(), "expected guest exit 23: %v; stderr: %s", err, stderr.String())
	gomega.Expect(exit.ExitCode()).To(gomega.Equal(23))
	gomega.Expect(stdout.Bytes()).To(gomega.Equal(payload))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("parity-stderr"))
}

// Entries are created after setup so recursive workspace chown cannot mask fallback ownership.
func assertMicrosandboxHostEntries(
	ctx context.Context,
	f *framework.Framework,
	hostPath, prefix string,
) {
	guestPath := filepath.Join("/workspaces", filepath.Base(hostPath))
	identity, err := f.DevsySSHOnce(ctx, hostPath, `printf '%s:%s' "$(id -u)" "$(id -g)"`)
	framework.ExpectNoError(err)
	identity = strings.TrimSpace(identity)

	for name, mode := range map[string]os.FileMode{prefix + "-file": 0o644, prefix + "-dir": 0o755} {
		hostEntry := filepath.Join(hostPath, name)
		if mode == 0o644 {
			//nolint:gosec // permission mirroring is the behavior under test
			err = os.WriteFile(hostEntry, []byte("host"), mode)
		} else {
			err = os.Mkdir(hostEntry, mode)
		}
		framework.ExpectNoError(err)
		//nolint:gosec // normalize host umask before checking guest-visible modes
		framework.ExpectNoError(os.Chmod(hostEntry, mode))
		ownerBefore := microsandboxHostOwner(ctx, hostEntry)
		result, err := f.DevsySSHOnce(
			ctx,
			hostPath,
			fmt.Sprintf("stat -c '%%u:%%g %%a' %s/%s", guestPath, name),
		)
		framework.ExpectNoError(err)
		gomega.Expect(strings.TrimSpace(result)).
			To(gomega.Equal(fmt.Sprintf("%s %o", identity, mode)))
		gomega.Expect(microsandboxHostOwner(ctx, hostEntry)).To(gomega.Equal(ownerBefore))
	}
}

func microsandboxHostOwner(ctx context.Context, entry string) string {
	args := []string{"-c", "%u:%g", entry}
	if runtime.GOOS == osDarwin {
		args = []string{"-f", "%u:%g", entry}
	}
	// #nosec G204 -- fixed stat executable and test-created path
	output, err := exec.CommandContext(ctx, "stat", args...).Output()
	framework.ExpectNoError(err)
	return strings.TrimSpace(string(output))
}

func microsandboxCreationTime(ctx context.Context, sandbox string) string {
	// #nosec G204 -- fixed inspect command and Devsy-generated sandbox name
	output, err := exec.CommandContext(ctx, "msb", "inspect", sandbox, "--format", "json").Output()
	framework.ExpectNoError(err)
	var info struct {
		CreatedAt string `json:"created_at"`
	}
	framework.ExpectNoError(json.Unmarshal(output, &info))
	gomega.Expect(info.CreatedAt).NotTo(gomega.BeEmpty())
	return info.CreatedAt
}
