package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

// DockerExecSubcommand is the docker subcommand used to exec into a container.
const DockerExecSubcommand = "exec"

// DockerExecArgs collects the inputs for BuildDockerExecArgs.
// User is optional; when set the lifecycle hook runs as that user, per
// the devcontainer spec (remoteUser). Without it, root-default exec cannot
// overwrite files previously created by the remoteUser on storage backends
// that do not honour CAP_DAC_OVERRIDE across UIDs.
// See https://containers.dev/implementors/json_reference/ (lifecycle-scripts).
type DockerExecArgs struct {
	Container       string
	User            string
	EnvArgs         []string
	WorkspaceFolder string
	Command         []string
}

// BuildDockerExecArgs assembles the argv passed to `docker exec`.
func BuildDockerExecArgs(a DockerExecArgs) []string {
	args := []string{DockerExecSubcommand}
	args = append(args, a.EnvArgs...)
	if a.WorkspaceFolder != "" {
		args = append(args, "--workdir", a.WorkspaceFolder)
	}
	if a.User != "" {
		args = append(args, "--user", a.User)
	}
	args = append(args, a.Container)
	if len(a.Command) == 1 {
		args = append(args, "sh", "-c", a.Command[0])
	} else {
		args = append(args, a.Command...)
	}
	return args
}

// LifecycleExecParams collects the inputs for ExecLifecycleHook.
type LifecycleExecParams struct {
	Ctx         context.Context
	Helper      *docker.DockerHelper
	ContainerID string
	EnvArgs     []string
	Workdir     string
	User        string
	// Recorder optionally retains each command's output and outcome. Nil
	// records nothing.
	Recorder *workspacelogs.Recorder
	// HookIndex is the merged-config position of the hook being run.
	HookIndex int
}

// EnvArgsRedactor masks sensitive values passed to docker exec as -e entries.
func EnvArgsRedactor(envArgs []string) *secrets.Redactor {
	entries := make([]string, 0, len(envArgs)/2)
	for _, arg := range envArgs {
		if arg != "-e" {
			entries = append(entries, arg)
		}
	}

	return secrets.NewEnvironmentRedactor(entries)
}

// ExecLifecycleHook runs a single devcontainer lifecycle hook against a container.
func ExecLifecycleHook(params *LifecycleExecParams, name string, hook types.LifecycleHook) error {
	if len(hook) == 0 {
		return nil
	}

	for key, command := range hook {
		if len(command) == 0 {
			continue
		}
		log.Debugf("running %s: %s %v", name, key, command)

		args := BuildDockerExecArgs(DockerExecArgs{
			Container:       params.ContainerID,
			User:            params.User,
			EnvArgs:         params.EnvArgs,
			WorkspaceFolder: params.Workdir,
			Command:         command,
		})
		rec := newLifecycleRecording(params, name, key)
		started := time.Now()
		err := params.Helper.Run(params.Ctx, args, docker.Streams{
			Stdin:  os.Stdin,
			Stdout: rec.tee(os.Stdout, workspacelogs.StreamStdout),
			Stderr: rec.tee(os.Stderr, workspacelogs.StreamStderr),
		})
		rec.finish(params.Ctx, time.Since(started), err)
		if err != nil {
			return fmt.Errorf("command %q failed%s: %w", key, rec.hint(), err)
		}
	}

	return nil
}

// lifecycleRecording retains one command's output and outcome. A nil value
// records nothing and passes streams through untouched.
type lifecycleRecording struct {
	rec            *workspacelogs.Recorder
	inv            workspacelogs.Invocation
	stdout, stderr io.WriteCloser
}

func newLifecycleRecording(params *LifecycleExecParams, hook, key string) *lifecycleRecording {
	if params.Recorder == nil {
		return nil
	}
	id := workspacelogs.NewID()
	index := params.HookIndex
	r := &lifecycleRecording{
		rec: params.Recorder,
		inv: workspacelogs.Invocation{
			ID: id, Phase: "lifecycle", Hook: hook, Index: &index, Key: key,
		},
		stdout: params.Recorder.Writer(id, workspacelogs.StreamStdout),
		stderr: params.Recorder.Writer(id, workspacelogs.StreamStderr),
	}
	r.record(workspacelogs.InvocationRunning, "")

	return r
}

// tee keeps dst as the only functional destination when nothing is recorded.
func (r *lifecycleRecording) tee(dst io.Writer, stream workspacelogs.Stream) io.Writer {
	if r == nil {
		return dst
	}
	if stream == workspacelogs.StreamStderr {
		return io.MultiWriter(dst, r.stderr)
	}

	return io.MultiWriter(dst, r.stdout)
}

func (r *lifecycleRecording) record(state workspacelogs.InvocationState, reason string) {
	inv := r.inv
	inv.State = state
	inv.Reason = reason
	r.rec.Invocation(inv)
}

// finish flushes output, then records the terminal state.
func (r *lifecycleRecording) finish(ctx context.Context, elapsed time.Duration, runErr error) {
	if r == nil {
		return
	}
	_ = r.stdout.Close()
	_ = r.stderr.Close()
	inv := r.inv
	inv.Duration = elapsed
	inv.State, inv.Reason = lifecycleOutcome(ctx, runErr)
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		if sig, isSig := strings.CutPrefix(exitErr.String(), "signal: "); isSig {
			inv.Signal = sig
		} else {
			code := exitErr.ExitCode()
			inv.ExitCode = &code
		}
	} else if runErr == nil {
		code := 0
		inv.ExitCode = &code
	}
	r.rec.Invocation(inv)
}

func lifecycleOutcome(ctx context.Context, runErr error) (workspacelogs.InvocationState, string) {
	switch {
	case runErr == nil:
		return workspacelogs.InvocationSucceeded, ""
	case ctx != nil && ctx.Err() != nil:
		return workspacelogs.InvocationCanceled, ""
	}
	if _, ok := errors.AsType[*exec.ExitError](runErr); !ok {
		return workspacelogs.InvocationFailed, "start_failed"
	}

	return workspacelogs.InvocationFailed, ""
}

// hint points at retained output, or is empty when no run is recorded.
func (r *lifecycleRecording) hint() string {
	if r == nil || r.rec.RunID() == "" {
		return ""
	}

	return fmt.Sprintf(
		" (full output: devsy workspace logs <workspace> --source setup --run %s --hook %s)",
		r.rec.RunID(), r.inv.Hook,
	)
}

// BuildLifecycleEnvArgs converts a merged config's RemoteEnv into -e KEY=VALUE
// pairs suitable for `docker exec`.
func BuildLifecycleEnvArgs(result *devcconfig.Result) []string {
	if result == nil || result.MergedConfig == nil {
		return nil
	}

	env := result.MergedConfig.RemoteEnv
	if len(env) == 0 {
		return nil
	}

	keys := make([]string, 0, len(env))
	for k, v := range env {
		if v != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	args := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		args = append(args, "-e", k+"="+*env[k])
	}
	return args
}
