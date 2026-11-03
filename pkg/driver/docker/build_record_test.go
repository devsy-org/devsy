package docker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
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

func lastInvocation(t *testing.T, recs []workspacelogs.Record) workspacelogs.Record {
	t.Helper()
	var last *workspacelogs.Record
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindInvocation {
			last = &recs[i]
		}
	}
	require.NotNil(t, last)

	return *last
}

func firstInvocation(t *testing.T, recs []workspacelogs.Record) workspacelogs.Record {
	t.Helper()
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindInvocation {
			return recs[i]
		}
	}
	require.Fail(t, "no invocation recorded")

	return workspacelogs.Record{}
}

const fakeKey = "fake"

type streamedFake struct {
	stdout, stderr, combined string
	err                      error
	key                      string
}

func (s streamedFake) build(context.Context, io.Writer, string, *build.BuildOptions) error {
	return errors.New("plain build must not be used")
}

func (s streamedFake) name() string    { return "fake build" }
func (s streamedFake) stepKey() string { return s.key }

func (s streamedFake) buildStreamed(
	_ context.Context,
	streams buildStreams,
	_ string,
	_ *build.BuildOptions,
) error {
	_, _ = streams.stdout.Write([]byte(s.stdout))
	_, _ = streams.stderr.Write([]byte(s.stderr))
	_, _ = streams.combined.Write([]byte(s.combined))

	return s.err
}

func TestExecuteBuildRetainsOutputAtWarnLevel(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)

	err := (&dockerDriver{}).executeBuild(
		ctx,
		streamedFake{stdout: "out line\n", stderr: "err line\n", combined: "merged", key: fakeKey},
		driver.BuildRequest{Options: provider.BuildOptions{Platform: "linux/arm64"}},
		&build.BuildOptions{},
	)

	require.NoError(t, err)
	assert.Empty(t, logs.All())
	recs := readRecords(t, store, runID)
	assert.Equal(t, "out line\n", outputText(recs, workspacelogs.StreamStdout))
	assert.Equal(t, "err line\n", outputText(recs, workspacelogs.StreamStderr))
	assert.Equal(t, "merged", outputText(recs, workspacelogs.StreamCombined))
	inv := lastInvocation(t, recs)
	assert.Equal(t, workspacelogs.InvocationSucceeded, inv.State)
	start := firstInvocation(t, recs)
	assert.Equal(t, workspacelogs.InvocationRunning, start.State)
	assert.Equal(t, fakeKey+" linux/arm64", start.HookKey)
	assert.Equal(t, "build", start.Phase)
	assert.Empty(t, start.Hook)
}

func TestExecuteBuildFailureKeepsErrorAndRecordsFailed(t *testing.T) {
	log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	cause := errors.New("solve exploded")

	err := (&dockerDriver{}).executeBuild(
		ctx,
		streamedFake{stderr: "boom\n", err: cause, key: fakeKey},
		driver.BuildRequest{},
		&build.BuildOptions{},
	)

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "fake build build: solve exploded")
	recs := readRecords(t, store, runID)
	assert.Equal(t, "boom\n", outputText(recs, workspacelogs.StreamStderr))
	assert.Equal(t, workspacelogs.InvocationFailed, lastInvocation(t, recs).State)
}

func TestExecuteBuildCanceledIsRecorded(t *testing.T) {
	log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	err := (&dockerDriver{}).executeBuild(
		ctx,
		streamedFake{err: context.Canceled, key: fakeKey},
		driver.BuildRequest{},
		&build.BuildOptions{},
	)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(
		t,
		workspacelogs.InvocationCanceled,
		lastInvocation(t, readRecords(t, store, runID)).State,
	)
}

func TestExecuteBuildWithoutRecorderUsesLoggerOnly(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)

	err := (&dockerDriver{}).executeBuild(
		t.Context(),
		streamedFake{stdout: "out\n", stderr: "err\n", combined: "both\n"},
		driver.BuildRequest{},
		&build.BuildOptions{},
	)

	require.NoError(t, err)
	var messages []string
	for _, e := range logs.All() {
		messages = append(messages, e.Message)
	}
	assert.Equal(t, []string{"build with fake build", "out", "err", "both"}, messages)
}

func writeBuildxScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker-fake")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --version) echo 'Docker version 24.0.7, build abc'; exit 0;;\n" +
		"esac\n" +
		"echo 'step output'\n" +
		"echo 'step error' >&2\n" +
		"exit 3\n"
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))

	return path
}

func TestBuildxKeepsStreamsSeparateAndErrorTail(t *testing.T) {
	log.InitTestObserved(t, zapcore.WarnLevel)
	ctx, store, runID := recordedContext(t)
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: writeBuildxScript(t)}}

	err := d.executeBuild(
		ctx,
		&dockerBuildxStrategy{driver: d},
		driver.BuildRequest{},
		&build.BuildOptions{Dockerfile: "Dockerfile", Context: "."},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to build image")
	assert.Contains(t, err.Error(), "step error")
	assert.NotContains(t, err.Error(), "step output")
	recs := readRecords(t, store, runID)
	assert.Equal(t, "step output\n", outputText(recs, workspacelogs.StreamStdout))
	assert.Equal(t, "step error\n", outputText(recs, workspacelogs.StreamStderr))
	inv := lastInvocation(t, recs)
	assert.Equal(t, workspacelogs.InvocationFailed, inv.State)
	assert.Equal(t, "docker-buildx", firstInvocation(t, recs).HookKey)
	require.NotNil(t, inv.ExitCode)
	assert.Equal(t, 3, *inv.ExitCode)
}

func TestStrategiesReportStepKeys(t *testing.T) {
	assert.Equal(t, "docker-buildx", (&dockerBuildxStrategy{}).stepKey())
	assert.Equal(t, "buildkit", (&buildkitStrategy{}).stepKey())
}
