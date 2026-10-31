package setup

import (
	"context"
	"fmt"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

type recordedRun struct {
	store *workspacelogs.Store
	runID string
	rec   *workspacelogs.Recorder
	ctx   context.Context
}

func newRecordedRun(t *testing.T) *recordedRun {
	t.Helper()
	store, err := workspacelogs.Open(workspacelogs.Options{Root: t.TempDir()})
	require.NoError(t, err)
	m, err := store.CreateRun(workspacelogs.RunSpec{Context: "default", Command: "up"})
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: store, RunID: m.RunID, Location: workspacelogs.LocationContainer,
	})
	require.NoError(t, err)

	return &recordedRun{
		store: store, runID: m.RunID, rec: rec,
		ctx: workspacelogs.WithRecorder(context.Background(), rec),
	}
}

func (r *recordedRun) records(t *testing.T) []workspacelogs.Record {
	t.Helper()
	r.rec.Close(workspacelogs.ProducerTerminal, true)
	resp, err := r.store.Read(workspacelogs.ReadRequest{
		RunID: r.runID, Limit: workspacelogs.MaxReadLimit,
	})
	require.NoError(t, err)

	return resp.Records
}

// invocation is the folded view of one invocation's records.
type invocation struct {
	hook, key, reason string
	index             *int
	states            []workspacelogs.InvocationState
	exitCode          *int
	stdout, stderr    string
}

func fold(recs []workspacelogs.Record) map[string]*invocation {
	out := map[string]*invocation{}
	for i := range recs {
		r := &recs[i]
		if r.InvocationID == "" {
			continue
		}
		inv := out[r.InvocationID]
		if inv == nil {
			inv = &invocation{}
			out[r.InvocationID] = inv
		}
		switch r.Kind {
		case workspacelogs.KindInvocation:
			inv.hook, inv.key, inv.index = r.Hook, r.HookKey, r.HookIndex
			inv.reason, inv.exitCode = r.Reason, r.ExitCode
			inv.states = append(inv.states, r.State)
		case workspacelogs.KindOutput:
			if r.Stream == workspacelogs.StreamStderr {
				inv.stderr += r.Text
			} else {
				inv.stdout += r.Text
			}
		}
	}

	return out
}

func (inv *invocation) final() workspacelogs.InvocationState {
	return inv.states[len(inv.states)-1]
}

func byKey(t *testing.T, recs []workspacelogs.Record, hook string) map[string]*invocation {
	t.Helper()
	out := map[string]*invocation{}
	for _, inv := range fold(recs) {
		if inv.hook == hook {
			slot := "-"
			if inv.index != nil {
				slot = fmt.Sprint(*inv.index)
			}
			out[slot+"/"+inv.key] = inv
		}
	}

	return out
}

func testParams(t *testing.T, ctx context.Context, hooks ...types.LifecycleHook) hookRunParams {
	t.Helper()
	u, err := user.Current()
	require.NoError(t, err)

	return hookRunParams{
		ctx:      ctx,
		commands: hooks,
		env:      lifecycleEnv{remoteUser: u.Username, workspaceFolder: t.TempDir()},
		name:     "testHooks",
		hook:     "postCreateCommand",
		plan:     &hookPlan{},
	}
}

func TestRecordsStringAndArgvCommands(t *testing.T) {
	run := newRecordedRun(t)
	p := testParams(t, run.ctx,
		types.LifecycleHook{"": {"echo out; echo err >&2"}},
		types.LifecycleHook{"": {"sh", "-c", "echo argv-out"}},
	)

	require.NoError(t, executeHookCommands(run.ctx, p, nil))

	got := byKey(t, run.records(t), "postCreateCommand")
	require.Len(t, got, 2)
	assert.Equal(t, "out\n", got["0/"].stdout)
	assert.Equal(t, "err\n", got["0/"].stderr)
	assert.Equal(t, "argv-out\n", got["1/"].stdout)
	for _, inv := range got {
		assert.Equal(t,
			[]workspacelogs.InvocationState{
				workspacelogs.InvocationRunning, workspacelogs.InvocationSucceeded,
			}, inv.states)
		require.NotNil(t, inv.exitCode)
		assert.Zero(t, *inv.exitCode)
	}
}

func TestRecordsParallelNamedCommandsKeepOwnOutput(t *testing.T) {
	run := newRecordedRun(t)
	p := testParams(t, run.ctx, types.LifecycleHook{
		"alpha": {"echo alpha-out"},
		"beta":  {"echo beta-out; exit 4"},
	})

	err := executeHookCommands(run.ctx, p, nil)
	require.Error(t, err)

	got := byKey(t, run.records(t), "postCreateCommand")
	require.Len(t, got, 2)
	assert.Equal(t, "alpha-out\n", got["0/alpha"].stdout)
	assert.Equal(t, "beta-out\n", got["0/beta"].stdout)
	assert.Equal(t, workspacelogs.InvocationSucceeded, got["0/alpha"].final())
	assert.Equal(t, workspacelogs.InvocationFailed, got["0/beta"].final())
	require.NotNil(t, got["0/beta"].exitCode)
	assert.Equal(t, 4, *got["0/beta"].exitCode)
}

func TestRecordsFailureEvenWhenCallerIgnoresError(t *testing.T) {
	run := newRecordedRun(t)
	p := testParams(t, run.ctx, types.LifecycleHook{"": {"echo boom >&2; exit 3"}})

	_ = executeHookCommands(run.ctx, p, nil)

	for _, inv := range byKey(t, run.records(t), "postCreateCommand") {
		assert.Equal(t, workspacelogs.InvocationFailed, inv.final())
		assert.Equal(t, "boom\n", inv.stderr)
		assert.Equal(t, 3, *inv.exitCode)
	}
}

func TestRecordsSilentSuccessHasNoOutput(t *testing.T) {
	run := newRecordedRun(t)
	p := testParams(t, run.ctx, types.LifecycleHook{"": {"true"}})

	require.NoError(t, executeHookCommands(run.ctx, p, nil))

	got := byKey(t, run.records(t), "postCreateCommand")
	require.Len(t, got, 1)
	assert.Equal(t, workspacelogs.InvocationSucceeded, got["0/"].final())
	assert.Empty(t, got["0/"].stdout+got["0/"].stderr)
}

func TestRecordsOutputAtQuietLogLevel(t *testing.T) {
	log.InitTestObserved(t, zapcore.ErrorLevel)
	run := newRecordedRun(t)
	p := testParams(t, run.ctx, types.LifecycleHook{"": {"echo kept"}})

	require.NoError(t, executeHookCommands(run.ctx, p, nil))

	assert.Equal(t, "kept\n", byKey(t, run.records(t), "postCreateCommand")["0/"].stdout)
}

func TestRecordsStartFailure(t *testing.T) {
	run := newRecordedRun(t)
	p := testParams(t, run.ctx, types.LifecycleHook{"": {"definitely-not-a-command-xyz", "arg"}})
	u, err := user.Current()
	require.NoError(t, err)
	p.env.remoteUser = u.Username

	require.Error(t, executeHookCommands(run.ctx, p, nil))

	inv := byKey(t, run.records(t), "postCreateCommand")["0/"]
	assert.Equal(t, workspacelogs.InvocationFailed, inv.final())
	assert.Equal(t, reasonCommandNotFound, inv.reason)
	assert.Nil(t, inv.exitCode)
}

func TestRecordsCancellation(t *testing.T) {
	run := newRecordedRun(t)
	ctx, cancel := context.WithCancel(run.ctx)
	p := testParams(t, ctx, types.LifecycleHook{"": {"sleep 30"}})
	time.AfterFunc(300*time.Millisecond, cancel)

	require.Error(t, executeHookCommands(ctx, p, nil))

	inv := byKey(t, run.records(t), "postCreateCommand")["0/"]
	assert.Equal(t, workspacelogs.InvocationCanceled, inv.final())
}

func TestRecordsMarkerSkipIsAlreadyAttempted(t *testing.T) {
	run := newRecordedRun(t)
	p := testParams(t, run.ctx, types.LifecycleHook{"": {"true"}})
	p.name = "recordTestMarker" + workspacelogs.NewID()
	p.content = "created-1"
	if _, err := markerExists(p.name, p.content); err != nil {
		t.Skipf("marker directory unavailable: %v", err)
	}

	require.NoError(t, runHook(p))
	require.NoError(t, runHook(p))

	inv := byKey(t, run.records(t), "postCreateCommand")
	require.Len(t, inv, 1)
	states := inv["0/"].states
	assert.Equal(t, workspacelogs.InvocationSkipped, states[len(states)-1])
	assert.Equal(t, reasonAlreadyAttempted, inv["0/"].reason)
}

func hooksResult(t *testing.T, dir string) *config.Result {
	t.Helper()
	u, err := user.Current()
	require.NoError(t, err)
	cmd := func(s string) []types.LifecycleHook {
		return []types.LifecycleHook{{"": {s}}}
	}

	return &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{RemoteUser: u.Username},
			UpdatedConfigProperties: config.UpdatedConfigProperties{
				OnCreateCommands:   cmd("echo created"),
				PostCreateCommands: cmd("echo later"),
				PostStartCommands:  cmd("echo started"),
			},
		},
		ContainerDetails:    &config.ContainerDetails{State: config.ContainerDetailsState{}},
		SubstitutionContext: &config.SubstitutionContext{ContainerWorkspaceFolder: dir},
	}
}

func TestRecordsPlanningSkipReasonsAndDeferred(t *testing.T) {
	run := newRecordedRun(t)
	result := hooksResult(t, t.TempDir())

	deferred, err := RunPreAttachHooks(run.ctx, result, PreAttachOptions{
		Skip: SkipPhases{PostStart: true},
	})
	require.NoError(t, err)
	require.NoError(t, deferred.Run())

	recs := run.records(t)
	create := byKey(t, recs, "onCreateCommand")["0/"]
	assert.Equal(t, []workspacelogs.InvocationState{
		workspacelogs.InvocationPending, workspacelogs.InvocationRunning,
		workspacelogs.InvocationSucceeded,
	}, create.states)
	assert.Equal(t, "created\n", create.stdout)

	content := byKey(t, recs, "updateContentCommand")["-/"]
	require.NotNil(t, content)
	assert.Equal(t, workspacelogs.InvocationSkipped, content.final())
	assert.Equal(t, reasonNoCommand, content.reason)

	// Same invocation ID before and after the background worker runs it.
	post := byKey(t, recs, "postCreateCommand")["0/"]
	assert.Equal(t, []workspacelogs.InvocationState{
		workspacelogs.InvocationPending, workspacelogs.InvocationDeferred,
		workspacelogs.InvocationRunning, workspacelogs.InvocationSucceeded,
	}, post.states)

	start := byKey(t, recs, "postStartCommand")["0/"]
	assert.Equal(t, reasonExplicitSkip, start.reason)
	assert.Empty(t, start.stdout)
}

func TestRecordsPrebuildExclusion(t *testing.T) {
	run := newRecordedRun(t)
	result := hooksResult(t, t.TempDir())

	_, err := RunPreAttachHooks(run.ctx, result, PreAttachOptions{Prebuild: true})
	require.NoError(t, err)

	recs := run.records(t)
	post := byKey(t, recs, "postCreateCommand")["0/"]
	assert.Equal(t, workspacelogs.InvocationSkipped, post.final())
	assert.Equal(t, reasonPrebuildExcluded, post.reason)
	assert.Equal(
		t,
		workspacelogs.InvocationSucceeded,
		byKey(t, recs, "onCreateCommand")["0/"].final(),
	)
}

func TestRecordsPostAttachSkipAndRun(t *testing.T) {
	run := newRecordedRun(t)
	result := hooksResult(t, t.TempDir())
	result.MergedConfig.PostAttachCommands = []types.LifecycleHook{{"": {"echo attached"}}}

	require.NoError(t, RunPostAttachHooks(run.ctx, result, PostAttachOptions{Skip: true}))
	require.NoError(t, RunPostAttachHooks(run.ctx, result, PostAttachOptions{}))

	var skipped, ran int
	for _, inv := range fold(run.records(t)) {
		switch inv.final() {
		case workspacelogs.InvocationSkipped:
			skipped++
			assert.Equal(t, reasonExplicitSkip, inv.reason)
		case workspacelogs.InvocationSucceeded:
			ran++
			assert.Equal(t, "attached\n", inv.stdout)
		}
	}
	assert.Equal(t, 1, skipped)
	assert.Equal(t, 1, ran)
}

func TestNoRecorderKeepsBehaviorAndErrorText(t *testing.T) {
	failing := types.LifecycleHook{"": {"echo oops >&2; exit 2"}}

	plain := testParams(t, context.Background(), failing)
	plainErr := executeHookCommands(context.Background(), plain, nil)
	require.Error(t, plainErr)
	assert.NotContains(t, plainErr.Error(), "full output")
	assert.Contains(t, plainErr.Error(), "oops")

	run := newRecordedRun(t)
	recorded := testParams(t, run.ctx, failing)
	recErr := executeHookCommands(run.ctx, recorded, nil)
	require.Error(t, recErr)
	assert.Contains(t, recErr.Error(), "oops")
	assert.Contains(
		t,
		recErr.Error(),
		"--source setup --run "+run.runID+" --hook postCreateCommand",
	)
	assert.True(t, strings.HasPrefix(recErr.Error(), "testHooks: command"))
}
