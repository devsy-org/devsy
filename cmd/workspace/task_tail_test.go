package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/require"
)

func newTestTailer(t *testing.T, taskID string) (*logTailer, string) {
	t.Helper()
	dir := t.TempDir()
	config.SetPathManager(fakeRuntimeDirPathManager{dir: dir})
	t.Cleanup(config.ResetPathManager)

	tailer := newLogTailer(taskID)
	t.Cleanup(func() { tailer.flush(io.Discard) })
	if tailer.path == "" {
		t.Fatal("newLogTailer produced an empty path")
	}
	return tailer, tailer.path
}

type fakeRuntimeDirPathManager struct {
	config.PathManager
	dir string
}

func (f fakeRuntimeDirPathManager) RuntimeDir() (string, error) { return f.dir, nil }

func (f fakeRuntimeDirPathManager) ProcessStreamsFile(name string) (string, error) {
	return filepath.Join(f.dir, name+".streams"), nil
}

func TestLogTailerEmitsLinesAsTheyAreAppended(t *testing.T) {
	tailer, path := newTestTailer(t, "task1")
	var out bytes.Buffer

	tailer.poll(&out) // file doesn't exist yet
	if out.Len() != 0 {
		t.Fatalf("expected no output before the file exists, got %q", out.String())
	}

	if err := os.WriteFile(path, []byte("first line\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tailer.poll(&out)
	if out.String() != "first line\n" {
		t.Fatalf("got %q, want %q", out.String(), "first line\n")
	}

	// #nosec G304 -- path is t.TempDir()-derived
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("second line\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = f.Close()

	tailer.poll(&out)
	if out.String() != "first line\nsecond line\n" {
		t.Fatalf("got %q, want both lines", out.String())
	}
}

func TestLogTailerSkipsStructuredEnvelopeLines(t *testing.T) {
	tailer, path := newTestTailer(t, "task2")
	content := `{"level":"debug","ts":"2026-01-01T00:00:00.000-0500","msg":"a debug line"}
{"kind":"status","schemaVersion":1,"phase":"building_image","state":"started"}
{"kind":"result","outcome":"success","containerId":"abc"}
plain unstructured line
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	tailer.poll(&out)

	got := out.String()
	if !bytes.Contains([]byte(got), []byte("a debug line")) {
		t.Errorf("expected the debug line to pass through, got %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("plain unstructured line")) {
		t.Errorf("expected the unstructured line to pass through, got %q", got)
	}
	if bytes.Contains([]byte(got), []byte(`"kind"`)) {
		t.Errorf("expected structured envelope lines to be filtered out, got %q", got)
	}
}

func TestLogTailerPreservesArbitraryJSON(t *testing.T) {
	tailer, path := newTestTailer(t, "task-json")
	content := `{"kind":"application","value":"keep this command output"}
{"kind":"status","schemaVersion":1,"phase":"running_command","state":"started"}
{"kind":"result","outcome":"success"}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	tailer.poll(&out)

	if got, want := out.String(), "{\"kind\":\"application\",\"value\":\"keep this command output\"}\n"; got != want {
		t.Fatalf("got %q, want arbitrary JSON to pass through as %q", got, want)
	}
}

func TestLogTailerFlushEmitsTrailingPartialLine(t *testing.T) {
	tailer, path := newTestTailer(t, "task3")
	// No trailing newline: simulates reading mid-write, or the worker's
	// final line never getting a newline before it exits.
	if err := os.WriteFile(path, []byte("unterminated"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	tailer.poll(&out)
	if out.Len() != 0 {
		t.Fatalf("expected the partial line to be buffered, not emitted yet, got %q", out.String())
	}

	tailer.flush(&out)
	if out.String() != "unterminated\n" {
		t.Fatalf("got %q, want %q", out.String(), "unterminated\n")
	}
}

func TestFollowTaskStreamsWorkerLogOutput(t *testing.T) {
	dir := t.TempDir()
	config.SetPathManager(fakeRuntimeDirPathManager{dir: dir})
	t.Cleanup(config.ResetPathManager)

	store, err := task.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	streamsPath, err := config.DefaultPathManager().
		ProcessStreamsFile(task.WorkerProcessName(tk.ID()))
	if err != nil {
		t.Fatalf("ProcessStreamsFile: %v", err)
	}
	line := `{"level":"debug","ts":"2026-01-01T00:00:00.000-0500","msg":"worker debug line"}` + "\n"
	if err := os.WriteFile(streamsPath, []byte(line), 0o600); err != nil {
		t.Fatalf("write streams file: %v", err)
	}

	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = tk.Succeed(nil)
	}()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	followErr := followTask(context.Background(), store, followTaskOptions{
		id:       tk.ID(),
		interval: 10 * time.Millisecond,
		emitJSON: true,
	})
	os.Stderr = origStderr
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if followErr != nil {
		t.Fatalf("followTask: %v", followErr)
	}
	if !strings.Contains(buf.String(), "worker debug line") {
		t.Errorf("expected the worker's debug line on stderr, got %q", buf.String())
	}
}

func TestLogTailerToleratesMissingRuntimeDir(t *testing.T) {
	config.SetPathManager(
		fakeRuntimeDirPathManager{dir: filepath.Join(t.TempDir(), "does-not-exist")},
	)
	t.Cleanup(config.ResetPathManager)

	tailer := newLogTailer("task4")
	var out bytes.Buffer
	tailer.poll(&out)
	tailer.flush(&out)
	if out.Len() != 0 {
		t.Errorf("expected no output when the streams file never appears, got %q", out.String())
	}
}

func TestLogTailerStreamsStatusLifecyclesSeparatelyFromLogs(t *testing.T) {
	tailer, path := newTestTailer(t, "timeline")
	var statuses, logs, worker bytes.Buffer
	tailer.statusOut = &statuses
	const parentID = "parent"
	events := []status.Event{
		{
			Phase:       status.PhasePreparingDevContainer,
			OperationID: parentID,
			State:       status.StateStarted,
		},
		{
			Phase: status.PhaseStartingContainer, OperationID: "child",
			ParentOperationID: parentID, State: status.StateStarted,
		},
		{
			Phase: status.PhaseStartingContainer, OperationID: "child",
			ParentOperationID: parentID, State: status.StateSucceeded, Duration: 2 * time.Second,
		},
		{
			Phase: status.PhasePreparingDevContainer, OperationID: parentID,
			State: status.StateSucceeded, Duration: 3 * time.Second,
		},
	}
	for _, event := range events {
		require.NoError(t, devcconfig.WriteStatusJSON(&worker, event))
	}
	worker.WriteString("worker log\n{\"kind\":\"result\",\"outcome\":\"success\"}\n")
	require.NoError(t, os.WriteFile(path, worker.Bytes(), 0o600))
	tailer.poll(&logs)
	tailer.flush(&logs)
	tailer.flush(&logs)
	lines := strings.Split(strings.TrimSpace(statuses.String()), "\n")
	require.Len(t, lines, len(events))
	for i, line := range lines {
		got, ok := devcconfig.ParseStatusLine(line)
		require.True(t, ok)
		require.Equal(t, events[i], got)
	}
	require.Equal(t, "worker log\n", logs.String())
}

func TestFollowTaskDrainsStatusBeforeResult(t *testing.T) {
	dir := t.TempDir()
	config.SetPathManager(fakeRuntimeDirPathManager{dir: dir})
	t.Cleanup(config.ResetPathManager)
	store, err := task.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	path, err := config.DefaultPathManager().ProcessStreamsFile(task.WorkerProcessName(tk.ID()))
	require.NoError(t, err)
	var worker bytes.Buffer
	for _, state := range []status.State{status.StateStarted, status.StateSucceeded} {
		require.NoError(t, devcconfig.WriteStatusJSON(&worker, status.Event{
			Phase: status.PhaseLaunchingIDE, OperationID: "ide", State: state,
		}))
	}
	// The final status line may still be buffered when the terminal task is read.
	require.NoError(t, os.WriteFile(path, bytes.TrimSuffix(worker.Bytes(), []byte("\n")), 0o600))
	require.NoError(t, tk.Succeed(nil))
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	original := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = original; _ = writer.Close() })
	followErr := followTask(context.Background(), store, followTaskOptions{
		id: tk.ID(), interval: time.Millisecond, emitJSON: true,
	})
	os.Stdout = original
	require.NoError(t, writer.Close())
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, followErr)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	require.Len(t, lines, 3)
	completion, ok := devcconfig.ParseStatusLine(lines[1])
	require.True(t, ok)
	require.Equal(t, status.StateSucceeded, completion.State)
	require.Equal(t, "ide", completion.OperationID)
	require.Contains(t, lines[2], `"kind":"result"`)
}

func TestFollowTaskReconcilesExitedWorker(t *testing.T) {
	config.SetPathManager(fakeRuntimeDirPathManager{dir: t.TempDir()})
	t.Cleanup(config.ResetPathManager)
	store, err := task.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	require.NoError(t, tk.HoldWorkerLock())
	require.NoError(t, tk.ReleaseWorkerLock())
	streamsPath, err := config.DefaultPathManager().
		ProcessStreamsFile(task.WorkerProcessName(tk.ID()))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(streamsPath, []byte("startup failed\n"), 0o600))
	r, w, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = original; _ = r.Close(); _ = w.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	followErr := followTask(
		ctx,
		store,
		followTaskOptions{id: tk.ID(), interval: time.Millisecond, emitJSON: true},
	)
	os.Stderr = original
	require.NoError(t, w.Close())
	logs, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Error(t, followErr)
	require.NotErrorIs(t, followErr, context.DeadlineExceeded)
	require.Equal(t, 1, strings.Count(string(logs), "startup failed"))
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, state.Status)
}

type failingReconciliationStore struct {
	*task.Store
	reconcile func(context.Context, string) (*task.State, error)
}

func (s failingReconciliationStore) ReconcileTask(
	ctx context.Context,
	id string,
) (*task.State, error) {
	return s.reconcile(ctx, id)
}

func TestFollowTaskRetriesCleanupBeforeTerminalFailure(t *testing.T) {
	config.SetPathManager(fakeRuntimeDirPathManager{dir: t.TempDir()})
	t.Cleanup(config.ResetPathManager)
	store, err := task.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	require.NoError(t, tk.HoldWorkerLock())
	require.NoError(t, tk.ReleaseWorkerLock())
	path, err := config.DefaultPathManager().ProcessStreamsFile(task.WorkerProcessName(tk.ID()))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("captured startup failure\n"), 0o600))
	transient := errors.New("transient cleanup failure")
	calls := 0
	wrapper := failingReconciliationStore{
		Store: store,
		reconcile: func(ctx context.Context, id string) (*task.State, error) {
			calls++
			if calls == 1 {
				return nil, transient
			}
			state, err := store.Get(id)
			require.NoError(t, err)
			require.False(t, state.Status.Terminal(), "cleanup failure must leave task retryable")
			return store.ReconcileTask(ctx, id)
		},
	}
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stderr
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = original; _ = reader.Close(); _ = writer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	followErr := followTask(
		ctx,
		wrapper,
		followTaskOptions{id: tk.ID(), interval: time.Millisecond, emitJSON: true},
	)
	os.Stderr = original
	require.NoError(t, writer.Close())
	logs, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(logs), "captured startup failure"))
	require.Error(t, followErr)
	require.NotErrorIs(t, followErr, transient)
	require.NotErrorIs(t, followErr, context.DeadlineExceeded)
	var classified *clierr.CLIError
	require.ErrorAs(t, followErr, &classified)
	require.Equal(t, 2, calls)
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, state.Status)
	require.Equal(t, state.Error, classified.Message)
}

func TestFollowTaskRetryHonorsCancellation(t *testing.T) {
	config.SetPathManager(fakeRuntimeDirPathManager{dir: t.TempDir()})
	t.Cleanup(config.ResetPathManager)
	store, err := task.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	tk, err := store.Create(task.CreateOptions{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	wrapper := failingReconciliationStore{
		Store: store,
		reconcile: func(context.Context, string) (*task.State, error) {
			calls++
			if calls == 2 {
				cancel()
			}
			return nil, errors.New("cleanup unavailable")
		},
	}
	err = followTask(ctx, wrapper, followTaskOptions{id: tk.ID(), interval: time.Millisecond})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 2, calls)
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.False(t, state.Status.Terminal())
}

func TestFollowTaskReconciliationDiagnostics(t *testing.T) {
	for _, level := range []string{"debug", "info"} {
		t.Run(level, func(t *testing.T) {
			config.SetPathManager(fakeRuntimeDirPathManager{dir: t.TempDir()})
			t.Cleanup(config.ResetPathManager)
			secret := strings.Repeat("test-value-", 8)
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			original := os.Stdout
			os.Stdout = writer
			t.Cleanup(func() { os.Stdout = original; _ = reader.Close(); _ = writer.Close() })
			log.InitTest(t)
			log.Init(
				log.Config{
					Level:    level,
					Format:   "json",
					Redactor: secrets.NewRedactor([]string{"TOKEN=" + secret}),
				},
			)
			var diagnostics bytes.Buffer
			remove := log.AddSink(&diagnostics)
			defer remove()
			// Each follower must report its own first failure, suppress consecutive
			// duplicates, and report a recurrence after successful reconciliation.
			for range 2 {
				runFollowerReconciliationSequence(t, secret)
			}
			_ = log.Sync()
			os.Stdout = original
			require.NoError(t, writer.Close())
			output, err := io.ReadAll(reader)
			require.NoError(t, err)
			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			require.Len(t, lines, 2, "only terminal result envelopes belong on stdout")
			for _, line := range lines {
				var envelope map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &envelope))
				require.Equal(t, "result", envelope["kind"])
				require.NotContains(t, line, "cleanup")
			}
			requireReconciliationDiagnostics(t, level, diagnostics.String(), secret)
		})
	}
}

func requireReconciliationDiagnostics(t *testing.T, level, diagnostics, secret string) {
	t.Helper()
	require.NotContains(t, diagnostics, secret)
	if level == "info" {
		require.Empty(t, diagnostics)
		return
	}
	records := strings.Split(strings.TrimSpace(diagnostics), "\n")
	require.Len(t, records, 6)
	for i, line := range records {
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		require.Equal(t, "debug", record["level"])
		require.NotContains(t, record, "kind")
		if i%3 == 1 {
			require.Contains(t, record["msg"], "lock unavailable")
		} else {
			require.Contains(t, record["msg"], "cleanup ***")
		}
	}
}

func runFollowerReconciliationSequence(t *testing.T, secret string) {
	t.Helper()
	store, err := task.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	tk, err := store.Create(task.CreateOptions{})
	require.NoError(t, err)
	sequence := []string{
		"cleanup " + secret,
		"cleanup " + secret,
		"lock unavailable",
		"lock unavailable",
		"",
		"cleanup " + secret,
		"cleanup " + secret,
	}
	calls := 0
	wrapper := failingReconciliationStore{
		Store: store,
		reconcile: func(_ context.Context, id string) (*task.State, error) {
			if calls == len(sequence) {
				require.NoError(t, tk.Succeed(nil))
				return store.Get(id)
			}
			message := sequence[calls]
			calls++
			if message == "" {
				return store.Get(id)
			}
			return nil, errors.New(message)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	followErr := followTask(
		ctx,
		wrapper,
		followTaskOptions{id: tk.ID(), interval: time.Millisecond, emitJSON: true},
	)
	cancel()
	require.NoError(t, followErr)
	require.Equal(t, len(sequence), calls)
}
