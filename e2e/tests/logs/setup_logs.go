package logs

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	hookPostCreate = "postCreateCommand"
	hookPostStart  = "postStartCommand"
	stateSucceeded = workspacelogs.InvocationSucceeded
	stateFailed    = workspacelogs.InvocationFailed
	pollTimeout    = 2 * time.Minute
	pollInterval   = 2 * time.Second
	defaultContext = "default"
	testdataBasic  = "tests/logs/testdata/setup-basic"
	testdataSecret = "tests/logs/testdata/setup-secret"
	// #nosec G101 -- test value, not a credential.
	secretValue      = "setup-logs-secret-93817"
	resultFormatFlag = "--result-format"
)

// eventually polls check until it passes, bounded by pollTimeout.
func eventually(ctx context.Context, check func(g gomega.Gomega)) {
	gomega.Eventually(check).
		WithContext(ctx).
		WithTimeout(pollTimeout).
		WithPolling(pollInterval).
		Should(gomega.Succeed())
}

// latestRunID returns the newest retained run of the workspace.
func latestRunID(ctx context.Context, f *framework.Framework, ws string) string {
	runs, err := f.DevsySetupRuns(ctx, ws)
	framework.ExpectNoError(err)
	gomega.Expect(runs.Runs).ToNot(gomega.BeEmpty(), "workspace should have a retained run")

	return runs.Runs[0].RunID
}

// onlyInvocation returns the single final invocation record of a hook.
func onlyInvocation(
	g gomega.Gomega,
	resp *workspacelogs.ReadResponse,
	hook string,
) workspacelogs.Record {
	// A deferred hook has a planning record plus a separate executed record.
	// Only the executed one carries the outcome.
	var executed []workspacelogs.Record
	for _, rec := range framework.SetupInvocationsFor(resp, hook) {
		if rec.State != workspacelogs.InvocationDeferred {
			executed = append(executed, rec)
		}
	}
	g.Expect(executed).To(gomega.HaveLen(1), "expected one executed %s invocation", hook)

	return executed[0]
}

var _ = ginkgo.Describe(
	"devsy setup logs test suite",
	ginkgo.Label("logs", "setup-logs"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("retains stdout and stderr of a foreground create without debug",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(testdataBasic, initialDir)
				framework.ExpectNoError(err)
				framework.ExpectNoError(f.DevsyUp(ctx, ws))

				stdout, _, err := f.DevsySetupLogs(ctx, ws, resultFormatFlag+"=plain")
				framework.ExpectNoError(err)
				gomega.Expect(stdout).To(gomega.ContainSubstring("stdout | out-marker-basic"))
				gomega.Expect(stdout).To(gomega.ContainSubstring("stderr | err-marker-basic"))

				resp, err := f.DevsySetupLogsRead(ctx, ws)
				framework.ExpectNoError(err)
				gomega.Expect(framework.SetupLogsText(resp, workspacelogs.StreamStdout)).
					To(gomega.ContainSubstring("out-marker-basic"))
				gomega.Expect(framework.SetupLogsText(resp, workspacelogs.StreamStderr)).
					To(gomega.ContainSubstring("err-marker-basic"))
				gomega.Expect(framework.SetupLogsText(resp, workspacelogs.StreamStdout)).
					ToNot(gomega.ContainSubstring("err-marker-basic"))
			})

		ginkgo.It("shows output before and after a delayed default waitFor hook",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(
					"tests/logs/testdata/setup-waitfor", initialDir,
				)
				framework.ExpectNoError(err)
				framework.ExpectNoError(f.DevsyUp(ctx, ws))

				eventually(ctx, func(g gomega.Gomega) {
					resp, readErr := f.DevsySetupLogsRead(ctx, ws)
					g.Expect(readErr).ToNot(gomega.HaveOccurred())
					g.Expect(framework.SetupLogsText(resp, "")).
						To(gomega.ContainSubstring("part-before"))
					g.Expect(framework.SetupLogsText(resp, "")).
						ToNot(gomega.ContainSubstring("part-after"))
				})

				// The hook waits for this file, so no test sleep is needed.
				_, err = f.DevsySSH(ctx, ws, "touch /tmp/release-hook")
				framework.ExpectNoError(err)

				eventually(ctx, func(g gomega.Gomega) {
					resp, readErr := f.DevsySetupLogsRead(ctx, ws)
					g.Expect(readErr).ToNot(gomega.HaveOccurred())
					text := framework.SetupLogsText(resp, "")
					g.Expect(text).To(gomega.ContainSubstring("part-before"))
					g.Expect(text).To(gomega.ContainSubstring("part-after"))
					g.Expect(onlyInvocation(g, resp, hookPostCreate).State).
						To(gomega.Equal(stateSucceeded))
				})
			})

		ginkgo.It("keeps early lines and the failure of a deferred hook",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(
					"tests/logs/testdata/setup-deferred-fail", initialDir,
				)
				framework.ExpectNoError(err)
				// A deferred hook failure does not fail the foreground up.
				framework.ExpectNoError(f.DevsyUp(ctx, ws))

				eventually(ctx, func(g gomega.Gomega) {
					resp, readErr := f.DevsySetupLogsRead(ctx, ws, "--hook", hookPostCreate)
					g.Expect(readErr).ToNot(gomega.HaveOccurred())
					inv := onlyInvocation(g, resp, hookPostCreate)
					g.Expect(inv.State).To(gomega.Equal(stateFailed))
					g.Expect(inv.ExitCode).ToNot(gomega.BeNil())
					g.Expect(*inv.ExitCode).To(gomega.Equal(1))
				})

				stdout, _, err := f.DevsySetupLogs(ctx, ws, "--hook", hookPostCreate,
					resultFormatFlag+"=plain")
				framework.ExpectNoError(err)
				// Line 1 is outside the last 200 lines the old failure tail kept.
				gomega.Expect(stdout).To(gomega.ContainSubstring("stdout | early-line-1\n"))
				gomega.Expect(stdout).To(gomega.ContainSubstring("stdout | early-line-300\n"))
				gomega.Expect(stdout).To(gomega.ContainSubstring("failed"))
			})

		ginkgo.It("records a marker skip on start and never reruns hooks on read",
			ginkgo.SpecTimeout(framework.TimeoutLong()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(
					"tests/logs/testdata/setup-start-twice", initialDir,
				)
				framework.ExpectNoError(err)
				framework.ExpectNoError(f.DevsyUp(ctx, ws))
				createRun := latestRunID(ctx, f, ws)

				framework.ExpectNoError(f.DevsyStop(ctx, ws))
				framework.ExpectNoError(f.DevsyUp(ctx, ws))
				startRun := latestRunID(ctx, f, ws)
				gomega.Expect(startRun).ToNot(gomega.Equal(createRun))

				created, err := f.DevsySetupLogsRead(ctx, ws, "--run", createRun)
				framework.ExpectNoError(err)
				gomega.Expect(framework.SetupLogsText(created, workspacelogs.StreamStdout)).
					To(gomega.ContainSubstring("create-output-marker"))

				started, err := f.DevsySetupLogsRead(ctx, ws, "--run", startRun)
				framework.ExpectNoError(err)
				skipped := onlyInvocation(gomega.Default, started, hookPostCreate)
				gomega.Expect(skipped.State).To(gomega.Equal(workspacelogs.InvocationSkipped))
				gomega.Expect(skipped.Reason).To(gomega.Equal("already_attempted"))
				gomega.Expect(framework.SetupLogsText(started, workspacelogs.StreamStdout)).
					To(gomega.ContainSubstring("start-output-marker"))
				gomega.Expect(onlyInvocation(gomega.Default, started, hookPostStart).State).
					To(gomega.Equal(stateSucceeded))

				// Repeated reads must not execute the hook again.
				for range 2 {
					_, _, err = f.DevsySetupLogs(ctx, ws, "--run", createRun)
					framework.ExpectNoError(err)
				}
				count, err := f.DevsySSH(ctx, ws, "wc -l < /tmp/create-count")
				framework.ExpectNoError(err)
				gomega.Expect(strings.TrimSpace(count)).To(gomega.Equal("1"))
			})

		ginkgo.It("keeps named parallel hooks apart",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(
					"tests/logs/testdata/setup-parallel", initialDir,
				)
				framework.ExpectNoError(err)
				framework.ExpectNoError(f.DevsyUp(ctx, ws))

				var alpha, beta workspacelogs.Record
				eventually(ctx, func(g gomega.Gomega) {
					resp, readErr := f.DevsySetupLogsRead(ctx, ws, "--hook", hookPostCreate)
					g.Expect(readErr).ToNot(gomega.HaveOccurred())
					byKey := map[string]workspacelogs.Record{}
					for _, inv := range framework.SetupInvocationsFor(resp, hookPostCreate) {
						byKey[inv.HookKey] = inv
					}
					g.Expect(byKey).To(gomega.HaveKey("alpha"))
					g.Expect(byKey).To(gomega.HaveKey("beta"))
					alpha, beta = byKey["alpha"], byKey["beta"]
					g.Expect(alpha.State).To(gomega.Equal(stateSucceeded))
					g.Expect(beta.State).To(gomega.Equal(stateFailed))
				})
				gomega.Expect(*beta.ExitCode).To(gomega.Equal(4))

				one, err := f.DevsySetupLogsRead(ctx, ws, "--invocation", alpha.InvocationID)
				framework.ExpectNoError(err)
				gomega.Expect(framework.SetupLogsText(one, "")).
					To(gomega.ContainSubstring("alpha-output"))
				gomega.Expect(framework.SetupLogsText(one, "")).
					ToNot(gomega.ContainSubstring("beta-output"))

				two, err := f.DevsySetupLogsRead(ctx, ws, "--invocation", beta.InvocationID)
				framework.ExpectNoError(err)
				gomega.Expect(framework.SetupLogsText(two, workspacelogs.StreamStderr)).
					To(gomega.ContainSubstring("beta-output"))
				gomega.Expect(framework.SetupLogsText(two, "")).
					ToNot(gomega.ContainSubstring("alpha-output"))
			})

		ginkgo.It("never exposes a known secret in output or stored files",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(testdataSecret, initialDir)
				framework.ExpectNoError(err)

				secretsDir, err := framework.CreateTempDir()
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func() { _ = os.RemoveAll(secretsDir) })
				secretsFile := secretsDir + "/secrets.json"
				payload, err := json.Marshal(map[string]string{"MY_SECRET": secretValue})
				framework.ExpectNoError(err)
				framework.ExpectNoError(os.WriteFile(secretsFile, payload, 0o600))

				framework.ExpectNoError(f.DevsyUp(ctx, ws, "--secrets-file", secretsFile))

				plain, plainErr, err := f.DevsySetupLogs(ctx, ws, resultFormatFlag+"=plain")
				framework.ExpectNoError(err)
				gomega.Expect(plain).To(gomega.ContainSubstring("secret-line="),
					"the hook output should be retained, with the value redacted")
				jsonOut, _, err := f.DevsySetupLogs(ctx, ws, resultFormatFlag+"=json")
				framework.ExpectNoError(err)
				for _, out := range []string{plain, plainErr, jsonOut} {
					gomega.Expect(out).ToNot(gomega.ContainSubstring(secretValue))
				}

				dir, err := framework.SetupLogsDir()
				framework.ExpectNoError(err)
				hits, err := framework.FindInFiles(dir, secretValue)
				framework.ExpectNoError(err)
				gomega.Expect(hits).To(gomega.BeEmpty(), "stored files must not hold the secret")
			})

		ginkgo.It("reads a run after the workspace is deleted",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(testdataBasic, initialDir)
				framework.ExpectNoError(err)
				framework.ExpectNoError(f.DevsyUp(ctx, ws))
				runID := latestRunID(ctx, f, ws)

				framework.ExpectNoError(f.DevsyWorkspaceDelete(ctx, ws))

				stdout, _, err := f.DevsySetupLogs(ctx, "--run", runID,
					"--context", defaultContext, resultFormatFlag+"=plain")
				framework.ExpectNoError(err)
				gomega.Expect(stdout).To(gomega.ContainSubstring("out-marker-basic"))

				runs, err := f.DevsySetupRuns(ctx, "--context", defaultContext)
				framework.ExpectNoError(err)
				var ids []string
				for _, run := range runs.Runs {
					ids = append(ids, run.RunID)
				}
				gomega.Expect(ids).To(gomega.ContainElement(runID))
			})

		ginkgo.It("reads cached history of a stopped workspace without starting it",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(testdataBasic, initialDir)
				framework.ExpectNoError(err)
				framework.ExpectNoError(f.DevsyUp(ctx, ws))
				framework.ExpectNoError(f.DevsyWorkspaceStop(ctx, ws))
				expectStopped(ctx, f, ws)

				stdout, _, err := f.DevsySetupLogs(ctx, ws, resultFormatFlag+"=plain")
				framework.ExpectNoError(err)
				gomega.Expect(stdout).To(gomega.ContainSubstring("out-marker-basic"))
				expectStopped(ctx, f, ws)
			})

		ginkgo.It("returns hook output shaped like protocol envelopes as plain data",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				ws, f, err := framework.SetupDockerWorkspace(testdataBasic, initialDir)
				framework.ExpectNoError(err)

				stdout, _, err := f.ExecCommandCapture(ctx, []string{
					"workspace", "up", ws, "--ide", "none", resultFormatFlag + "=json",
				})
				framework.ExpectNoError(err)
				// Status lines precede the result; every line must stay valid JSON.
				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				for _, line := range lines {
					gomega.Expect(json.Valid([]byte(line))).To(gomega.BeTrue(), line)
				}
				result := map[string]any{}
				framework.ExpectNoError(json.Unmarshal([]byte(lines[len(lines)-1]), &result))
				gomega.Expect(result["kind"]).To(gomega.Equal("result"))
				runID, _ := result["runId"].(string)
				gomega.Expect(runID).To(gomega.Equal(latestRunID(ctx, f, ws)))

				resp, err := f.DevsySetupLogsRead(ctx, ws, "--run", runID)
				framework.ExpectNoError(err)
				text := framework.SetupLogsText(resp, workspacelogs.StreamStdout)
				gomega.Expect(text).To(gomega.ContainSubstring(`{"kind":"status"`))
				gomega.Expect(text).To(gomega.ContainSubstring(`{"kind":"result"`))
				gomega.Expect(onlyInvocation(gomega.Default, resp, hookPostCreate).State).
					To(gomega.Equal(stateSucceeded))
			})
	},
)

func expectStopped(ctx context.Context, f *framework.Framework, ws string) {
	status, err := f.DevsyStatus(ctx, ws)
	framework.ExpectNoError(err)
	framework.ExpectEqual(strings.ToUpper(status.State), "STOPPED")
}
