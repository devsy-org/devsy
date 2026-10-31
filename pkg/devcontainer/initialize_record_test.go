package devcontainer

import (
	"context"
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

func recordedInitContext(t *testing.T) (context.Context, func() []workspacelogs.Record) {
	t.Helper()
	store, err := workspacelogs.Open(workspacelogs.Options{Root: t.TempDir()})
	require.NoError(t, err)
	m, err := store.CreateRun(workspacelogs.RunSpec{Context: "default", Command: "up"})
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: store, RunID: m.RunID, Location: workspacelogs.LocationHost,
	})
	require.NoError(t, err)

	return workspacelogs.WithRecorder(context.Background(), rec), func() []workspacelogs.Record {
		rec.Close(workspacelogs.ProducerTerminal, true)
		resp, readErr := store.Read(workspacelogs.ReadRequest{RunID: m.RunID})
		require.NoError(t, readErr)

		return resp.Records
	}
}

func initConf(cmds map[string][]string) *config.DevContainerConfig {
	conf := &config.DevContainerConfig{}
	conf.InitializeCommand = types.LifecycleHook(cmds)

	return conf
}

type initSummary struct {
	states         []workspacelogs.InvocationState
	exitCode       *int
	reason         string
	stdout, stderr string
}

func summarizeInit(recs []workspacelogs.Record) map[string]*initSummary {
	byID := map[string]*initSummary{}
	keys := map[string]string{}
	for i := range recs {
		r := &recs[i]
		if r.InvocationID == "" {
			continue
		}
		s := byID[r.InvocationID]
		if s == nil {
			s = &initSummary{}
			byID[r.InvocationID] = s
		}
		switch r.Kind {
		case workspacelogs.KindInvocation:
			keys[r.InvocationID] = r.HookKey
			s.states = append(s.states, r.State)
			s.exitCode, s.reason = r.ExitCode, r.Reason
		case workspacelogs.KindOutput:
			if r.Stream == workspacelogs.StreamStderr {
				s.stderr += r.Text
			} else {
				s.stdout += r.Text
			}
		}
	}
	out := map[string]*initSummary{}
	for id, s := range byID {
		out[keys[id]] = s
	}

	return out
}

func TestInitializeCommandRecordsOutputAtErrorLevel(t *testing.T) {
	log.InitTestObserved(t, zapcore.ErrorLevel)
	ctx, records := recordedInitContext(t)
	conf := initConf(map[string][]string{
		"a": {"echo a-out; echo a-err >&2"},
		"b": {"echo b-out"},
	})

	require.NoError(t, runInitializeCommand(ctx, t.TempDir(), conf, nil))

	got := summarizeInit(records())
	require.Len(t, got, 2)
	assert.Equal(t, "a-out\n", got["a"].stdout)
	assert.Equal(t, "a-err\n", got["a"].stderr)
	assert.Equal(t, "b-out\n", got["b"].stdout)
	assert.Empty(t, got["b"].stderr)
	for _, s := range got {
		assert.Equal(t, []workspacelogs.InvocationState{
			workspacelogs.InvocationRunning, workspacelogs.InvocationSucceeded,
		}, s.states)
	}
}

func TestInitializeCommandRecordsFailure(t *testing.T) {
	ctx, records := recordedInitContext(t)
	conf := initConf(map[string][]string{"": {"echo bad >&2; exit 5"}})

	require.Error(t, runInitializeCommand(ctx, t.TempDir(), conf, nil))

	s := summarizeInit(records())[""]
	require.NotNil(t, s)
	assert.Equal(t, workspacelogs.InvocationFailed, s.states[len(s.states)-1])
	require.NotNil(t, s.exitCode)
	assert.Equal(t, 5, *s.exitCode)
	assert.Equal(t, "bad\n", s.stderr)
}

func TestInitializeCommandRecordsStartFailure(t *testing.T) {
	ctx, records := recordedInitContext(t)
	conf := initConf(map[string][]string{"": {"definitely-not-a-command-xyz", "x"}})

	require.Error(t, runInitializeCommand(ctx, t.TempDir(), conf, nil))

	s := summarizeInit(records())[""]
	require.NotNil(t, s)
	assert.Equal(t, workspacelogs.InvocationFailed, s.states[len(s.states)-1])
	assert.Equal(t, "start_failed", s.reason)
	assert.Nil(t, s.exitCode)
}

func TestInitializeCommandRecordsCancellation(t *testing.T) {
	ctx, records := recordedInitContext(t)
	ctx, cancel := context.WithCancel(ctx)
	time.AfterFunc(300*time.Millisecond, cancel)
	conf := initConf(map[string][]string{"": {"sleep 30"}})

	require.Error(t, runInitializeCommand(ctx, t.TempDir(), conf, nil))

	s := summarizeInit(records())[""]
	require.NotNil(t, s)
	assert.Equal(t, workspacelogs.InvocationCanceled, s.states[len(s.states)-1])
}

func TestInitializeCommandWithoutRecorderStillRuns(t *testing.T) {
	conf := initConf(map[string][]string{"": {"echo ok"}})
	require.NoError(t, runInitializeCommand(context.Background(), t.TempDir(), conf, nil))
	conf = initConf(map[string][]string{"": {"exit 2"}})
	require.Error(t, runInitializeCommand(context.Background(), t.TempDir(), conf, nil))
}
