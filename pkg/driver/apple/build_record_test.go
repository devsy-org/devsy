package apple

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

type streamingClient struct {
	mockClient
	stdout, stderr string
	err            error
}

func (c *streamingClient) Run(_ context.Context, _ []string, s apple.Streams) error {
	_, _ = s.Stdout.Write([]byte(c.stdout))
	_, _ = s.Stderr.Write([]byte(c.stderr))

	return c.err
}

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

func invocationRecords(recs []workspacelogs.Record) []workspacelogs.Record {
	var out []workspacelogs.Record
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindInvocation {
			out = append(out, recs[i])
		}
	}

	return out
}

func TestExecuteBuildRetainsOutputAtWarnLevel(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	d := &appleDriver{Apple: &streamingClient{stdout: "built layer\n", stderr: "warning text"}}

	err := d.executeBuild(ctx, &build.BuildOptions{Dockerfile: "Dockerfile"}, "linux/arm64")

	require.NoError(t, err)
	assert.Empty(t, logs.All())
	recs := readRecords(t, store, runID)
	assert.Equal(t, "built layer\n", outputText(recs, workspacelogs.StreamStdout))
	assert.Equal(t, "warning text", outputText(recs, workspacelogs.StreamStderr))
	inv := invocationRecords(recs)
	require.Len(t, inv, 2)
	assert.Equal(t, "apple linux/arm64", inv[0].HookKey)
	assert.Equal(t, "build", inv[0].Phase)
	assert.Equal(t, workspacelogs.InvocationSucceeded, inv[1].State)
}

func TestExecuteBuildFailureKeepsErrorTail(t *testing.T) {
	log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	cause := errors.New("exit status 2")
	d := &appleDriver{Apple: &streamingClient{stderr: "no space left\n", err: cause}}

	err := d.executeBuild(ctx, &build.BuildOptions{}, "")

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "failed to build image: exit status 2: no space left")
	recs := readRecords(t, store, runID)
	assert.Equal(t, "no space left\n", outputText(recs, workspacelogs.StreamStderr))
	inv := invocationRecords(recs)
	require.Len(t, inv, 2)
	assert.Equal(t, "apple", inv[0].HookKey)
	assert.Equal(t, workspacelogs.InvocationFailed, inv[1].State)
}

func TestExecuteBuildCanceledIsRecorded(t *testing.T) {
	log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	d := &appleDriver{Apple: &streamingClient{err: context.Canceled}}

	err := d.executeBuild(ctx, &build.BuildOptions{}, "")

	require.ErrorIs(t, err, context.Canceled)
	inv := invocationRecords(readRecords(t, store, runID))
	require.Len(t, inv, 2)
	assert.Equal(t, workspacelogs.InvocationCanceled, inv[1].State)
}

func TestExecuteBuildWithoutRecorderLogsOnly(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.InfoLevel)
	d := &appleDriver{Apple: &streamingClient{stdout: "plain\n"}}

	require.NoError(t, d.executeBuild(t.Context(), &build.BuildOptions{}, ""))

	var messages []string
	for _, e := range logs.All() {
		messages = append(messages, e.Message)
	}
	assert.Equal(t, []string{"plain"}, messages)
}

func TestResolveExistingImageRecordsReuse(t *testing.T) {
	ctx, store, runID := recordedContext(t)
	d := &appleDriver{Apple: &mockClient{}}

	info, found := d.resolveExistingImage(ctx, "img", driver.BuildRequest{
		ExtendedBuildInfo: &feature.ExtendedBuildInfo{},
	})

	require.True(t, found)
	assert.Equal(t, "img", info.ImageName)
	inv := invocationRecords(readRecords(t, store, runID))
	require.Len(t, inv, 1)
	assert.Equal(t, workspacelogs.InvocationSkipped, inv[0].State)
	assert.Equal(t, "image_reused", inv[0].Reason)
}
