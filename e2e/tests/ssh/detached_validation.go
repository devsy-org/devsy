package ssh

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("detached workspace input validation", ginkgo.Label("task-cancel"), func() {
	ginkgo.It(
		"rejects invalid explicit IDs without submitting tasks",
		ginkgo.SpecTimeout(time.Minute),
		func(ctx context.Context) {
			wd, err := os.Getwd()
			framework.ExpectNoError(err)
			f := framework.NewDefaultFramework(filepath.Join(wd, "bin"))
			home, err := os.MkdirTemp("", "devsy-invalid-id-")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(os.RemoveAll, home)
			cli := detachedValidationCLI{framework: f, home: home}
			for _, id := range []string{"Backend", "backend_name", "backend.api", "has spaces", strings.Repeat("a", 49)} {
				out, err := cli.command(ctx,
					cmdWorkspace,
					"up",
					"--detach",
					"--result-format=json",
					"--id",
					id,
				)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(out).To(gomega.ContainSubstring("workspace name"))
				gomega.Expect(out).NotTo(gomega.ContainSubstring(`"kind":"task"`))
			}
			out, err := cli.command(ctx, cmdWorkspace, "task", "list")
			framework.ExpectNoError(err)
			var states []detachedUpState
			framework.ExpectNoError(json.Unmarshal([]byte(out), &states))
			gomega.Expect(states).To(gomega.BeEmpty())
		},
	)

	ginkgo.It(
		"records early provider failure and preserves a mixed-case local source",
		ginkgo.SpecTimeout(time.Minute),
		func(ctx context.Context) {
			wd, err := os.Getwd()
			framework.ExpectNoError(err)
			f := framework.NewDefaultFramework(filepath.Join(wd, "bin"))
			home, err := os.MkdirTemp("", "devsy-early-worker-")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(os.RemoveAll, home)
			cli := detachedValidationCLI{framework: f, home: home}
			source := filepath.Join(home, "Backend.API")
			framework.ExpectNoError(os.MkdirAll(source, 0o700))
			sentinel := filepath.Join(source, "KeepCase.txt")
			framework.ExpectNoError(os.WriteFile(sentinel, []byte("original source"), 0o600))
			out, err := cli.command(ctx,
				cmdWorkspace,
				"up",
				"--detach",
				"--result-format=json",
				"--provider=missing-provider-1471",
				source,
			)
			framework.ExpectNoError(err)
			var envelope struct {
				Kind string `json:"kind"`
				ID   string `json:"id"`
			}
			framework.ExpectNoError(json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope))
			gomega.Expect(envelope.Kind).To(gomega.Equal("task"))
			var states []struct {
				ID          string `json:"id"`
				Status      string `json:"status"`
				WorkspaceID string `json:"workspaceId"`
				Error       string `json:"error"`
			}
			gomega.Eventually(func() string {
				out, err := cli.command(ctx, cmdWorkspace, "task", "list")
				if err != nil {
					return err.Error()
				}
				if err := json.Unmarshal([]byte(out), &states); err != nil {
					return err.Error()
				}
				for _, state := range states {
					if state.ID == envelope.ID {
						return state.Status
					}
				}
				return "missing"
			}).WithContext(ctx).WithTimeout(20 * time.Second).WithPolling(100 * time.Millisecond).Should(gomega.Equal("failed"))
			gomega.Expect(states).To(gomega.HaveLen(1))
			gomega.Expect(states[0].WorkspaceID).To(gomega.Equal(workspace.ToID(source)))
			gomega.Expect(states[0].Error).To(gomega.ContainSubstring("prepare workspace client"))
			gomega.Expect(states[0].Error).NotTo(gomega.ContainSubstring("missing-provider-1471"))
			cli.expectFailureEnvelope(ctx, envelope.ID, "prepare workspace client")
			sourceRoot, err := os.OpenRoot(source)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(sourceRoot.Close)
			contents, err := sourceRoot.ReadFile("KeepCase.txt")
			framework.ExpectNoError(err)
			gomega.Expect(string(contents)).To(gomega.Equal("original source"))
			entries, err := os.ReadDir(home)
			framework.ExpectNoError(err)
			var entryNames []string
			for _, entry := range entries {
				entryNames = append(entryNames, entry.Name())
			}
			gomega.Expect(entryNames).To(gomega.ContainElement("Backend.API"))
			gomega.Expect(entryNames).NotTo(gomega.ContainElement("backend.api"))
		},
	)
})

type detachedValidationCLI struct {
	framework *framework.Framework
	home      string
}

func (cli detachedValidationCLI) newCommand(ctx context.Context, args ...string) *exec.Cmd {
	// #nosec G204 -- repository-built CLI fixture with spec-controlled arguments and temporary paths.
	cmd := exec.CommandContext(
		ctx,
		filepath.Join(cli.framework.DevsyBinDir, cli.framework.DevsyBinName),
		args...)
	cmd.Env = detachedValidationEnvironment(cli.home)
	cmd.WaitDelay = time.Second
	return cmd
}

func (cli detachedValidationCLI) command(ctx context.Context, args ...string) (string, error) {
	out, err := cli.newCommand(ctx, args...).CombinedOutput()
	return string(out), err
}

var _ = ginkgo.Describe(
	"detached workspace early worker errors",
	ginkgo.Label("task-cancel"),
	func() {
		for _, stage := range []string{"config", "client"} {
			ginkgo.It(
				"persists early "+stage+" failure for JSON followers",
				ginkgo.SpecTimeout(time.Minute),
				func(ctx context.Context) {
					wd, err := os.Getwd()
					framework.ExpectNoError(err)
					f := framework.NewDefaultFramework(filepath.Join(wd, "bin"))
					home, err := os.MkdirTemp("", "devsy-worker-error-")
					framework.ExpectNoError(err)
					ginkgo.DeferCleanup(os.RemoveAll, home)
					cli := detachedValidationCLI{framework: f, home: home}
					store, err := task.NewStoreAt(filepath.Join(home, "state", "tasks"))
					framework.ExpectNoError(err)
					tk, err := store.Create(task.CreateOptions{Command: "up"})
					framework.ExpectNoError(err)
					framework.ExpectNoError(tk.BeginLaunch())
					expected := "prepare workspace client"
					args := []string{
						cmdWorkspace,
						"up",
						"--task-id",
						tk.ID(),
						"--result-format=json",
						"--id=Backend",
					}
					if stage == "config" {
						framework.ExpectNoError(
							os.WriteFile(
								filepath.Join(home, "config.yaml"),
								[]byte("contexts: ["),
								0o600,
							),
						)
						expected = "load devsy config"
					}
					worker := cli.newCommand(ctx, args...)
					framework.ExpectNoError(worker.Start())
					framework.ExpectNoError(tk.SetPID(worker.Process.Pid))
					gomega.Expect(worker.Wait()).To(gomega.HaveOccurred())
					state, err := store.Get(tk.ID())
					framework.ExpectNoError(err)
					gomega.Expect(state.Status).To(gomega.Equal(task.StatusFailed))
					gomega.Expect(state.Error).To(gomega.ContainSubstring(expected))
					cli.expectFailureEnvelope(ctx, tk.ID(), expected)
				},
			)
		}
	},
)

func (cli detachedValidationCLI) expectFailureEnvelope(ctx context.Context, id, diagnostic string) {
	followCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := cli.command(followCtx,
		cmdWorkspace,
		"task",
		"logs",
		id,
		"--follow",
		"--result-format=json",
	)
	gomega.Expect(err).To(gomega.HaveOccurred())
	gomega.Expect(followCtx.Err()).NotTo(gomega.HaveOccurred())
	var envelope struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	framework.ExpectNoError(json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope))
	gomega.Expect(envelope.Kind).To(gomega.Equal("error"))
	gomega.Expect(envelope.Message).To(gomega.ContainSubstring(diagnostic))
	gomega.Expect(out).NotTo(gomega.ContainSubstring("missing-provider-1471"))
}

func detachedValidationEnvironment(home string) []string {
	return append(os.Environ(), "DEVSY_HOME="+home, "DEVSY_TEST_TOKEN=missing-provider-1471")
}
