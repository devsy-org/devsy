package ssh

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/onsi/gomega"
)

// detachedUpState mirrors the `workspace task list` JSON fields the detached
// up specs poll on.
type detachedUpState struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Phase     string `json:"phase"`
	ErrorCode string `json:"errorCode"`
	Error     string `json:"error"`
	PID       int    `json:"pid"`
}

// setupTunnelProvider registers the docker provider; Windows runs use the
// podman runtime installed by CI.
func setupTunnelProvider(ctx context.Context, initialDir string) *framework.Framework {
	binDir := initialDir + "/bin"
	if runtime.GOOS == osWindows {
		f, err := framework.SetupDockerProvider(binDir, "podman")
		framework.ExpectNoError(err)
		return f
	}
	f := framework.NewDefaultFramework(binDir)
	_ = f.DevsyProviderAdd(ctx, "docker")
	framework.ExpectNoError(f.DevsyProviderUse(ctx, "docker"))
	return f
}

// startDetachedTunnelUp submits `workspace up --detach --ssh-tunnel` and
// returns the task ID from the envelope on stdout.
func startDetachedTunnelUp(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
	extraArgs ...string,
) (string, error) {
	args := []string{
		cmdWorkspace, "up",
		names.Flag(names.Debug),
		names.Flag(names.IDE), "none",
		names.Flag(names.SSHTunnel),
		names.Flag(names.Detach),
		names.Flag(names.ResultFormat), "json",
	}
	args = append(args, extraArgs...)
	args = append(args, workspace)

	out, err := f.ExecCommandOutput(ctx, args)
	if err != nil {
		return "", err
	}
	var env struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return "", fmt.Errorf("parse task envelope %q: %w", out, err)
	}
	if env.Kind != "task" || env.ID == "" {
		return "", fmt.Errorf("unexpected task envelope %q", line)
	}
	return env.ID, nil
}

func findDetachedUpTask(
	ctx context.Context,
	f *framework.Framework,
	taskID string,
) (detachedUpState, error) {
	out, err := f.DevsyWorkspaceTaskList(ctx)
	if err != nil {
		return detachedUpState{}, err
	}
	var states []detachedUpState
	if err := json.Unmarshal([]byte(out), &states); err != nil {
		return detachedUpState{}, fmt.Errorf("parse task list: %w", err)
	}
	for _, s := range states {
		if s.ID == taskID {
			return s, nil
		}
	}
	return detachedUpState{}, fmt.Errorf("task %s not in task list", taskID)
}

// waitDetachedTunnelReady polls the task until the up pipeline reports the
// ready phase, which it reaches only with the tunnel active.
func waitDetachedTunnelReady(
	ctx context.Context,
	f *framework.Framework,
	taskID string,
) detachedUpState {
	var state detachedUpState
	gomega.Eventually(func() (string, error) {
		var err error
		state, err = findDetachedUpTask(ctx, f, taskID)
		if err != nil {
			return "", err
		}
		if state.Status == "failed" {
			return "", gomega.StopTrying("detached up task failed: " + state.Error)
		}
		return state.Phase, nil
	}).WithContext(ctx).WithTimeout(tunnelActiveTimeout).WithPolling(2 * time.Second).
		Should(gomega.Equal("ready"))
	return state
}

// waitDetachedTaskCanceled polls until the task records the canceled error
// code and returns the final state.
func waitDetachedTaskCanceled(
	ctx context.Context,
	f *framework.Framework,
	taskID string,
) detachedUpState {
	var state detachedUpState
	gomega.Eventually(func() string {
		var err error
		state, err = findDetachedUpTask(ctx, f, taskID)
		if err != nil {
			return ""
		}
		return state.ErrorCode
	}).WithContext(ctx).WithTimeout(30 * time.Second).WithPolling(time.Second).
		Should(gomega.Equal("canceled"))
	return state
}
