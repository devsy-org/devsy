package buildkit

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/moby/buildkit/client"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func recordedContext(t *testing.T) (context.Context, *workspacelogs.Store, string) {
	t.Helper()
	store, err := workspacelogs.Open(workspacelogs.Options{Root: t.TempDir()})
	require.NoError(t, err)
	m, err := store.CreateRun(workspacelogs.RunSpec{
		Context: "default", WorkspaceUID: "uid", Command: "up",
	})
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: store, RunID: m.RunID, Location: workspacelogs.LocationProviderHost,
	})
	require.NoError(t, err)

	return workspacelogs.WithRecorder(t.Context(), rec), store, m.RunID
}

func readRecords(t *testing.T, store *workspacelogs.Store, runID string) []workspacelogs.Record {
	t.Helper()
	var all []workspacelogs.Record
	req := workspacelogs.ReadRequest{RunID: runID}
	for {
		resp, err := store.Read(req)
		require.NoError(t, err)
		all = append(all, resp.Records...)
		if !resp.HasMore {
			return all
		}
		req.After = resp.NextCursor
	}
}

func outputText(recs []workspacelogs.Record, stream workspacelogs.Stream) string {
	var b strings.Builder
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindOutput && recs[i].Stream == stream {
			b.WriteString(recs[i].Text)
		}
	}

	return b.String()
}

func invocations(recs []workspacelogs.Record) []workspacelogs.Record {
	var out []workspacelogs.Record
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindInvocation {
			out = append(out, recs[i])
		}
	}

	return out
}

func TestStepRetainsOutputBelowLogLevel(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	logWriter := log.Writer(log.LevelInfo)
	step := StartStep(ctx, "buildkit")

	w := step.Tee(logWriter, workspacelogs.StreamCombined)
	_, err := w.Write([]byte("step 1/2\nlast line without newline"))
	require.NoError(t, err)
	_ = logWriter.Close()
	step.Finish(ctx, nil)

	assert.Empty(t, logs.All(), "logger at warn drops info output")
	recs := readRecords(t, store, runID)
	assert.Equal(
		t,
		"step 1/2\nlast line without newline",
		outputText(recs, workspacelogs.StreamCombined),
	)
	inv := invocations(recs)
	require.Len(t, inv, 2)
	assert.Equal(t, workspacelogs.InvocationRunning, inv[0].State)
	assert.Equal(t, "build", inv[0].Phase)
	assert.Empty(t, inv[0].Hook)
	assert.Equal(t, "buildkit", inv[0].HookKey)
	assert.Equal(t, workspacelogs.InvocationSucceeded, inv[1].State)
}

func TestStepFinishStates(t *testing.T) {
	exitErr := exec.Command("sh", "-c", "exit 5").Run()
	var ee *exec.ExitError
	require.ErrorAs(t, exitErr, &ee)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		name     string
		err      error
		ctxFor   func(context.Context) context.Context
		state    workspacelogs.InvocationState
		wantExit bool
	}{
		{
			name:     "failed with exit code",
			err:      exitErr,
			state:    workspacelogs.InvocationFailed,
			wantExit: true,
		},
		{name: "failed", err: errors.New("boom"), state: workspacelogs.InvocationFailed},
		{
			name:   "canceled",
			err:    context.Canceled,
			ctxFor: func(context.Context) context.Context { return canceled },
			state:  workspacelogs.InvocationCanceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, store, runID := recordedContext(t)
			step := StartStep(ctx, "buildkit")
			_, _ = step.Tee(&bytes.Buffer{}, workspacelogs.StreamStderr).Write([]byte("err tail"))
			finishCtx := ctx
			if tt.ctxFor != nil {
				finishCtx = tt.ctxFor(ctx)
			}
			step.Finish(finishCtx, tt.err)

			recs := readRecords(t, store, runID)
			inv := invocations(recs)
			require.Len(t, inv, 2)
			assert.Equal(t, tt.state, inv[1].State)
			assert.Equal(t, tt.wantExit, inv[1].ExitCode != nil)
			if tt.wantExit {
				assert.Equal(t, 5, *inv[1].ExitCode)
			}
			assert.Equal(t, "err tail", outputText(recs, workspacelogs.StreamStderr))
		})
	}
}

func TestStepWithoutRecorderForwardsUnchanged(t *testing.T) {
	step := StartStep(t.Context(), "buildkit")
	assert.Nil(t, step)
	var buf bytes.Buffer

	w := step.Tee(&buf, workspacelogs.StreamStdout)
	_, _ = w.Write([]byte("plain"))
	step.Finish(t.Context(), errors.New("ignored"))

	assert.Same(t, &buf, w)
	assert.Equal(t, "plain", buf.String())
	RecordSkipped(t.Context(), "image", "image_reused")
}

func TestRecordSkippedWritesReason(t *testing.T) {
	ctx, store, runID := recordedContext(t)

	RecordSkipped(ctx, "image", "image_reused")

	inv := invocations(readRecords(t, store, runID))
	require.Len(t, inv, 1)
	assert.Equal(t, workspacelogs.InvocationSkipped, inv[0].State)
	assert.Equal(t, "image_reused", inv[0].Reason)
	assert.Equal(t, "build", inv[0].Phase)
}

// The printer renders on its own goroutine, so the last status must be drained
// before the recorder writer closes.
func TestPrinterDrainKeepsFinalBytes(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	step := StartStep(ctx, "buildkit-remote")
	dgst := digest.FromString("vertex")
	now := time.Now()

	pw, err := NewPrinter(ctx, step.Tee(log.Writer(log.LevelInfo), workspacelogs.StreamCombined))
	require.NoError(t, err)
	pw.Status() <- &client.SolveStatus{
		Vertexes: []*client.Vertex{{Digest: dgst, Name: "RUN make", Started: &now}},
		Logs: []*client.VertexLog{
			{Vertex: dgst, Stream: 2, Data: []byte("fatal: final error line\n"), Timestamp: now},
		},
	}
	close(pw.Status())
	<-pw.Done()
	step.Finish(ctx, errors.New("solve failed"))

	assert.Empty(t, logs.All())
	text := outputText(readRecords(t, store, runID), workspacelogs.StreamCombined)
	assert.Contains(t, text, "fatal: final error line")
	assert.Contains(t, text, "RUN make")
}

func TestStepMasksBuildSecretsInRecordedOutput(t *testing.T) {
	ctx, store, runID := recordedContext(t)
	step := StartStep(ctx, "buildkit", "TOKEN=hunter2-value")

	w := step.Tee(&bytes.Buffer{}, workspacelogs.StreamCombined)
	_, err := w.Write([]byte("using hunter2-value here\nsplit hunter2"))
	require.NoError(t, err)
	_, err = w.Write([]byte("-value end"))
	require.NoError(t, err)
	step.Finish(ctx, nil)

	text := outputText(readRecords(t, store, runID), workspacelogs.StreamCombined)
	assert.NotContains(t, text, "hunter2")
	assert.Contains(t, text, "using ")
	assert.Contains(t, text, " end")
}
