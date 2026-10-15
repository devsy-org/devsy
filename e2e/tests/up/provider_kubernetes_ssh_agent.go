package up

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"Kubernetes workspace private SSH Git clone",
	ginkgo.Label("up-provider-kubernetes"),
	func() {
		ginkgo.It(
			"succeeds on first creation, repeated up, and recreate with SSH agent",
			func(ctx ginkgo.SpecContext) {
				gomega.Expect(runtime.GOOS).
					To(gomega.Equal("linux"), "private SSH fixture requires Linux host access to the Kind Docker network")
				initialDir, err := os.Getwd()
				framework.ExpectNoError(err)
				f := framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
				authSock, pubKey, stopAgent := framework.StartMockSSHAgent(ginkgo.GinkgoT())
				ginkgo.DeferCleanup(stopAgent)
				ginkgo.GinkgoT().Setenv("SSH_AUTH_SOCK", authSock)
				id := fmt.Sprintf("sshgit-%d", time.Now().UnixNano())
				namespace := id
				selector := "devsy.sh/created=true,issue-1423=" + id
				source, sha := privateSSHGitFixture(ctx, initialDir, id, pubKey)

				fixtureCommand(ctx, 2*time.Minute, nil, "kubectl", "create", "namespace", namespace)
				ginkgo.DeferCleanup(func(specCtx ginkgo.SpecContext) {
					cleanupCtx, cancel := context.WithTimeout(
						context.WithoutCancel(specCtx),
						time.Minute,
					)
					defer cancel()
					fixtureCommand(
						cleanupCtx,
						2*time.Minute,
						nil,
						"kubectl",
						"delete",
						"namespace",
						namespace,
						"--wait=false",
					)
				})
				framework.ExpectNoError(f.DevsyProviderAdd(ctx, "kubernetes", "--name", id,
					"-o", "KUBERNETES_NAMESPACE="+namespace, "-o", "LABELS=issue-1423="+id))
				ginkgo.DeferCleanup(func(specCtx ginkgo.SpecContext) {
					cleanupCtx, cancel := context.WithTimeout(
						context.WithoutCancel(specCtx),
						time.Minute,
					)
					defer cancel()
					framework.ExpectNoError(f.DevsyProviderDelete(cleanupCtx, id))
				})
				ginkgo.DeferCleanup(func(specCtx ginkgo.SpecContext) {
					cleanupCtx, cancel := context.WithTimeout(
						context.WithoutCancel(specCtx),
						time.Minute,
					)
					defer cancel()
					framework.ExpectNoError(f.CleanupWorkspace(cleanupCtx, id))
				})
				ginkgo.DeferCleanup(func(specCtx ginkgo.SpecContext) {
					cleanupCtx, cancel := context.WithTimeout(
						context.WithoutCancel(specCtx),
						time.Minute,
					)
					defer cancel()
					if !ginkgo.CurrentSpecReport().Failed() {
						return
					}
					for _, args := range [][]string{
						{"describe", "pods", "-n", namespace},
						{"get", "events", "-n", namespace, "--sort-by=.lastTimestamp"},
						{"logs", "-n", namespace, "-l", selector, "--all-containers", "--tail=200"},
					} {
						output, err := runFixtureCommand(
							cleanupCtx,
							2*time.Minute,
							nil,
							append([]string{"kubectl"}, args...)...)
						ginkgo.GinkgoWriter.Printf(
							"Kubernetes failure diagnostics: %s\n%s\nerror: %v\n",
							args,
							output,
							err,
						)
					}
				})

				// Check the pod-to-service route before testing Devsy, so fixture failures
				// cannot be confused with a first-up failure.
				host, _, _ := strings.Cut(strings.TrimPrefix(source, "ssh://git@"), "/")
				fixtureCommand(
					ctx,
					2*time.Minute,
					nil,
					"kubectl",
					"run",
					"git-route",
					"-n",
					namespace,
					"--image=alpine:3.22",
					"--restart=Never",
					"--command",
					"--",
					"sh",
					"-c",
					"nc -z -w 10 "+host+" 22",
				)
				gomega.Eventually(func() string {
					pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					return strings.TrimSpace(
						fixtureCommand(
							pollCtx,
							2*time.Minute,
							nil,
							"kubectl",
							"get",
							"pod",
							"git-route",
							"-n",
							namespace,
							"-o",
							"jsonpath={.status.phase}",
						),
					)
				}).WithTimeout(time.Minute).WithPolling(time.Second).Should(
					gomega.Equal("Succeeded"),
					"private SSH Git service must be reachable from Kind pods",
				)

				up := func(phase string, args ...string) {
					commandCtx, cancel := context.WithTimeout(ctx, framework.TimeoutModerate())
					defer cancel()
					// The clone override applies only to the current up invocation.
					baseArgs := []string{
						"workspace", "up", "--debug", "--ide", "none",
						"--pull-from-inside-container=true",
					}
					stdout, stderr, err := f.ExecCommandCapture(
						commandCtx,
						append(baseArgs, args...),
					)
					gomega.Expect(err).
						NotTo(gomega.HaveOccurred(), "%s first CLI attempt: stdout=%s stderr=%s", phase, stdout, stderr)
					for _, failure := range []string{
						"chown ssh agent sock file",
						"fatal: Unable to read current working directory",
						"error configuring git credentials",
						"start workspace: ",
					} {
						gomega.Expect(stdout+stderr).
							NotTo(gomega.ContainSubstring(failure), "%s", phase)
					}
					if phase == "recreate" {
						gomega.Expect(stdout+stderr).
							To(gomega.ContainSubstring("cloning repository"),
								"recreate must clone the private SSH source inside the pod")
					}
				}
				verifyCheckout := func() {
					commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
					defer cancel()
					out, err := f.DevsySSHOnce(
						commandCtx,
						id,
						"test -d .git && test -f /tmp/devsy-1423-post-start && cat expected.txt && git rev-parse HEAD",
					)
					framework.ExpectNoError(err)
					gomega.Expect(out).
						To(gomega.ContainSubstring("private SSH source cloned successfully"))
					gomega.Expect(out).To(gomega.ContainSubstring(sha))
				}
				up(
					"create",
					source,
					"--provider",
					id,
					"--id",
					id,
				)
				firstPod := fixtureWorkspacePod(ctx, namespace, selector)
				verifyCheckout()
				up("repeat", id)
				samePod := fixtureWorkspacePod(ctx, namespace, selector)
				gomega.Expect(samePod.UID).
					To(gomega.Equal(firstPod.UID), "second up must keep the pod")
				verifyCheckout()
				up("recreate", id, "--recreate")
				recreated := fixtureWorkspacePod(ctx, namespace, selector)
				gomega.Expect(recreated.UID).
					NotTo(gomega.Equal(firstPod.UID), "recreate must replace the pod")
				verifyCheckout()
				verifyKindSSHAgentCleanup(ctx, f, id, pubKey)
			},
			ginkgo.SpecTimeout(framework.TimeoutLong()),
		)
	},
)

func privateSSHGitFixture(
	ctx context.Context,
	initialDir, id, pubKey string,
) (string, string) {
	fixtureDir, err := framework.CopyToTempDir("tests/up/testdata/kubernetes-ssh-agent")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, fixtureDir)
	buildDir, err := os.MkdirTemp("", "devsy-private-ssh-git-")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(os.RemoveAll, buildDir)
	sha := createKindSSHGitRepository(ctx, fixtureDir)
	fixtureCommand(
		ctx,
		2*time.Minute,
		nil,
		"cp",
		"-R",
		fixtureDir,
		filepath.Join(buildDir, "repository"),
	)
	authorizedKey := "restrict,command=\"git-upload-pack /srv/repository.git\" " + pubKey + "\n"
	framework.ExpectNoError(
		os.WriteFile(filepath.Join(buildDir, "authorized_keys"), []byte(authorizedKey), 0o600),
	)
	// #nosec G304 -- fixed test fixture path rooted at the suite working directory.
	dockerfile, err := os.ReadFile(
		filepath.Join(initialDir, "tests/up/testdata/kubernetes-ssh-agent.Dockerfile"),
	)
	framework.ExpectNoError(err)
	// #nosec G703 -- buildDir is a test-owned directory returned by os.MkdirTemp.
	framework.ExpectNoError(os.WriteFile(filepath.Join(buildDir, "Dockerfile"), dockerfile, 0o600))
	host := startKindSSHGitServer(ctx, buildDir, id)
	source := "ssh://git@" + host + "/srv/repository.git"
	sshOptions := "ssh -F /dev/null -o BatchMode=yes -o ConnectTimeout=5" +
		" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o IdentityFile=none"
	env := []string{"SSH_AUTH_SOCK=" + os.Getenv("SSH_AUTH_SOCK"), "GIT_SSH_COMMAND=" + sshOptions}
	gomega.Eventually(func() error {
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, err := runFixtureCommand(
			pollCtx,
			2*time.Minute,
			env,
			"git",
			"ls-remote",
			source,
			"refs/heads/main",
		)
		if err != nil {
			return fmt.Errorf("authenticated fixture readiness: %w: %s", err, out)
		}
		if !strings.Contains(out, sha) {
			return fmt.Errorf("fixture HEAD mismatch: %s", out)
		}
		return nil
	}).WithTimeout(time.Minute).WithPolling(time.Second).Should(gomega.Succeed())
	out, err := runFixtureCommand(
		ctx,
		2*time.Minute,
		[]string{"SSH_AUTH_SOCK=", "GIT_SSH_COMMAND=" + sshOptions + " -o IdentityAgent=none"},
		"git",
		"ls-remote",
		source,
	)
	gomega.Expect(err).
		To(gomega.HaveOccurred(), "private repository must deny unauthenticated ls-remote: %s", out)
	gomega.Expect(out).
		To(gomega.ContainSubstring("Permission denied"), "unauthenticated failure must be authentication rejection")
	return source, sha
}

func createKindSSHGitRepository(ctx context.Context, fixtureDir string) string {
	fixtureCommand(
		ctx,
		2*time.Minute,
		nil,
		"git",
		"-C",
		fixtureDir,
		"init",
		"--initial-branch=main",
	)
	fixtureCommand(ctx, 2*time.Minute, nil, "git", "-C", fixtureDir, "add", ".")
	fixtureCommand(
		ctx,
		2*time.Minute,
		nil,
		"git",
		"-C",
		fixtureDir,
		"-c",
		"user.name=Devsy E2E",
		"-c",
		"user.email=e2e@example.invalid",
		"-c",
		"commit.gpgsign=false",
		"commit",
		"-m",
		"Private SSH fixture",
	)
	sha := strings.TrimSpace(
		fixtureCommand(ctx, 2*time.Minute, nil, "git", "-C", fixtureDir, "rev-parse", "HEAD"),
	)
	return sha
}

func startKindSSHGitServer(ctx context.Context, buildDir, id string) string {
	image := "devsy-private-ssh-git:" + id
	fixtureCommand(ctx, 2*time.Minute, nil, "docker", "build", "-t", image, buildDir)
	ginkgo.DeferCleanup(
		func(specCtx ginkgo.SpecContext) {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(specCtx), time.Minute)
			defer cancel()
			fixtureCommand(cleanupCtx, 2*time.Minute, nil, "docker", "image", "rm", image)
		},
	)
	fixtureCommand(
		ctx,
		2*time.Minute,
		nil,
		"docker",
		"run",
		"-d",
		"--name",
		id,
		"--network",
		"kind",
		image,
	)
	ginkgo.DeferCleanup(func(specCtx ginkgo.SpecContext) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(specCtx), time.Minute)
		defer cancel()
		if ginkgo.CurrentSpecReport().Failed() {
			out, err := runFixtureCommand(cleanupCtx, 2*time.Minute, nil, "docker", "logs", id)
			ginkgo.GinkgoWriter.Printf("private SSH Git service logs:\n%s\nerror: %v\n", out, err)
		}
		fixtureCommand(cleanupCtx, 2*time.Minute, nil, "docker", "rm", "-f", id)
	})
	host := strings.TrimSpace(
		fixtureCommand(
			ctx,
			2*time.Minute,
			nil,
			"docker",
			"inspect",
			"--format",
			"{{(index .NetworkSettings.Networks \"kind\").IPAddress}}",
			id,
		),
	)
	gomega.Expect(host).
		NotTo(gomega.BeEmpty(), "private SSH fixture requires the Kind Docker bridge")
	return host
}

func verifyKindSSHAgentCleanup(
	ctx context.Context,
	f *framework.Framework,
	id, pubKey string,
) {
	controlDir, err := os.MkdirTemp("", "devsy-kind-cm-")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(os.RemoveAll, controlDir)
	controlPath := filepath.Join(controlDir, "cm.sock")
	host := id + ".devsy"
	env := []string{"SSH_AUTH_SOCK=" + os.Getenv("SSH_AUTH_SOCK")}
	options := []string{
		"-o",
		"ControlPath=" + controlPath,
		"-o",
		"StrictHostKeyChecking=no",
		"-o",
		"UserKnownHostsFile=/dev/null",
		"-o",
		"ConnectTimeout=10",
	}
	fixtureCommand(
		ctx,
		2*time.Minute,
		env,
		append([]string{cmdSSH},
			append(
				options,
				"-o",
				"ForwardAgent=yes",
				"-o",
				"ControlMaster=yes",
				"-o",
				"ControlPersist=60",
				"-N",
				"-f",
				host,
			)...)...)
	closed := false
	closeMaster := func(cleanupCtx context.Context) {
		fixtureCommand(
			cleanupCtx,
			2*time.Minute,
			env,
			append([]string{cmdSSH},
				append(options, "-O", "exit", host)...)...)
	}
	ginkgo.DeferCleanup(func(specCtx ginkgo.SpecContext) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(specCtx), time.Minute)
		defer cancel()
		if !closed {
			closeMaster(cleanupCtx)
		}
	})
	remote := func(command string) string {
		return fixtureCommand(
			ctx,
			2*time.Minute,
			env,
			append([]string{cmdSSH},
				append(options, host, "--", command)...)...)
	}
	socket := strings.TrimSpace(remote("printf %s \"$SSH_AUTH_SOCK\""))
	gomega.Expect(socket).NotTo(gomega.BeEmpty())
	gomega.Expect(filepath.Base(filepath.Dir(socket))).To(gomega.HavePrefix("devsy-ssh-agent-"))
	remote("test -S " + shellescape.Quote(socket))
	keys := remote("ssh-add -L")
	gomega.Expect(keys).To(gomega.ContainSubstring(strings.Fields(pubKey)[1]))
	closeMaster(ctx)
	closed = true
	waitForKindSSHSocketCleanup(ctx, f, id, socket)
}

func waitForKindSSHSocketCleanup(ctx context.Context, f *framework.Framework, id, socket string) {
	gomega.Eventually(func() error {
		pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err := f.DevsySSHOnce(pollCtx, id, "test ! -e "+shellescape.Quote(filepath.Dir(socket)))
		return err
	}).WithTimeout(30*time.Second).WithPolling(500*time.Millisecond).Should(
		gomega.Succeed(),
		"closed connection socket directory %s must be removed",
		filepath.Dir(socket),
	)
}
