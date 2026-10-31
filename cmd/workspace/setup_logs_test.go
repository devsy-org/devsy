package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	clientpkg "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testCtx     = "default"
	testUID     = "uid-1"
	testSecret  = "s3cr3t-token-value"
	errCombined = "cannot be combined"
	hookCreate  = "postCreateCommand"
)

type fakeRefresher struct {
	reason string
	calls  int
	// onRefresh may add records to the store, like a successful import.
	onRefresh func(call int)
}

func (f *fakeRefresher) refresh(context.Context, *workspacelogs.Store, string) string {
	f.calls++
	if f.onRefresh != nil {
		f.onRefresh(f.calls)
	}

	return f.reason
}

func newStore(t *testing.T) *workspacelogs.Store {
	t.Helper()
	s, err := workspacelogs.Open(workspacelogs.Options{Root: t.TempDir()})
	require.NoError(t, err)

	return s
}

type seededRun struct {
	runID    string
	recorder *workspacelogs.Recorder
	inv      string
}

func seedRun(t *testing.T, s *workspacelogs.Store, uid string) *seededRun {
	t.Helper()
	m, err := s.CreateRun(workspacelogs.RunSpec{
		Context: testCtx, WorkspaceUID: uid, WorkspaceIDAtStart: "ws", Command: "up",
	})
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: s, RunID: m.RunID, Location: workspacelogs.LocationContainer,
		Redactor: secrets.NewRedactor([]string{"TOKEN=" + testSecret}),
	})
	require.NoError(t, err)

	return &seededRun{runID: m.RunID, recorder: rec, inv: workspacelogs.NewID()}
}

func (r *seededRun) hook(state workspacelogs.InvocationState, exit *int) {
	r.recorder.Invocation(workspacelogs.Invocation{
		ID: r.inv, Phase: "setup", Hook: hookCreate, State: state, ExitCode: exit,
	})
}

func (r *seededRun) write(stream workspacelogs.Stream, text string) {
	w := r.recorder.Writer(r.inv, stream)
	_, _ = w.Write([]byte(text))
	_ = w.Close()
}

func (r *seededRun) finish() { r.recorder.Close(workspacelogs.ProducerTerminal, true) }

type harness struct {
	reader *setupReader
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

func newHarness(s *workspacelogs.Store, f setupLogsFlags, ref refresher, jsonMode bool) *harness {
	h := &harness{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	h.reader = &setupReader{
		store: s, json: jsonMode, out: h.out, errOut: h.errOut, refresher: ref, flags: f,
	}

	return h
}

func target() setupTarget {
	return setupTarget{context: testCtx, workspaceID: "ws", uid: testUID, named: true}
}

type validateCase struct {
	name    string
	flags   setupLogsFlags
	args    []string
	format  string
	wantErr string
}

func runValidateCases(t *testing.T, tests []validateCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &LogsCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: formatPlain}}
			if tt.format != "" {
				cmd.ResultFormat = tt.format
			}
			cmd.setupLogsFlags = tt.flags

			err := cmd.validateSource(tt.args)

			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidateSourceRejectsSetupOnlyFlagsForContainer(t *testing.T) {
	run := workspacelogs.NewID()
	runValidateCases(t, []validateCase{
		{name: "container default", flags: setupLogsFlags{Source: "container"}},
		{name: "empty source", flags: setupLogsFlags{}},
		{
			name:    "unknown source",
			flags:   setupLogsFlags{Source: "bogus"},
			wantErr: "unknown --source",
		},
		{
			name:    "list with container",
			flags:   setupLogsFlags{List: true},
			wantErr: "--list requires",
		},
		{name: "run with container", flags: setupLogsFlags{RunID: run}, wantErr: "--run requires"},
		{
			name:    "hook with container",
			flags:   setupLogsFlags{Hook: hookCreate},
			wantErr: "--hook requires",
		},
		{
			name:    "follow with container",
			flags:   setupLogsFlags{Follow: true},
			wantErr: "--follow requires",
		},
		{
			name:    "limit with container",
			flags:   setupLogsFlags{Limit: 5},
			wantErr: "--limit requires",
		},
		{
			name:    "after with container",
			flags:   setupLogsFlags{After: "x"},
			wantErr: "--after requires",
		},
	})
}

func TestValidateSourceSetupSelectors(t *testing.T) {
	run := workspacelogs.NewID()
	runValidateCases(t, []validateCase{
		{name: "setup latest", flags: setupLogsFlags{Source: sourceSetup}, args: []string{"ws"}},
		{
			name:  "setup explicit run no workspace",
			flags: setupLogsFlags{Source: sourceSetup, RunID: run},
		},
		{
			name:    "setup no workspace needs run",
			flags:   setupLogsFlags{Source: sourceSetup},
			wantErr: "explicit run ID",
		},
		{
			name:    "setup no workspace rejects latest",
			flags:   setupLogsFlags{Source: sourceSetup, RunID: "latest"},
			wantErr: "explicit run ID",
		},
		{name: "setup list no workspace", flags: setupLogsFlags{Source: sourceSetup, List: true}},
	})
}

func TestValidateSourceSetupConflictsAndBadValues(t *testing.T) {
	run := workspacelogs.NewID()
	runValidateCases(t, []validateCase{
		{
			name:    "list with run",
			flags:   setupLogsFlags{Source: sourceSetup, List: true, RunID: run},
			args:    []string{"ws"},
			wantErr: errCombined,
		},
		{
			name:    "list with hook",
			flags:   setupLogsFlags{Source: sourceSetup, List: true, Hook: hookCreate},
			wantErr: errCombined,
		},
		{
			name:    "list with follow",
			flags:   setupLogsFlags{Source: sourceSetup, List: true, Follow: true},
			wantErr: errCombined,
		},
		{
			name:    "list limit too large",
			flags:   setupLogsFlags{Source: sourceSetup, List: true, Limit: 101},
			wantErr: "--limit must be between",
		},
		{
			name:    "read limit too large",
			flags:   setupLogsFlags{Source: sourceSetup, Limit: 2001},
			args:    []string{"ws"},
			wantErr: "limit must be",
		},
	})
}

func TestValidateSourceSetupRejectsBadValues(t *testing.T) {
	runValidateCases(t, []validateCase{
		{
			name:    "unknown hook",
			flags:   setupLogsFlags{Source: sourceSetup, Hook: "nope"},
			args:    []string{"ws"},
			wantErr: "unknown hook",
		},
		{
			name:    "bad run id",
			flags:   setupLogsFlags{Source: sourceSetup, RunID: "../etc/passwd"},
			args:    []string{"ws"},
			wantErr: "invalid identifier",
		},
		{
			name:    "bad invocation id",
			flags:   setupLogsFlags{Source: sourceSetup, Invocation: "x"},
			args:    []string{"ws"},
			wantErr: "invalid identifier",
		},
		{
			name:    "bad cursor",
			flags:   setupLogsFlags{Source: sourceSetup, After: "a b;rm"},
			args:    []string{"ws"},
			wantErr: "malformed cursor",
		},
		{
			name:    "two workspaces",
			flags:   setupLogsFlags{Source: sourceSetup},
			args:    []string{"a", "b"},
			wantErr: "at most one",
		},
		{
			name:    "bad result format",
			flags:   setupLogsFlags{Source: sourceSetup},
			args:    []string{"ws"},
			format:  "yaml",
			wantErr: "unexpected output format",
		},
	})
}

func TestCompletedRunReadsWithoutRefresh(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	exit := 3
	run.hook(workspacelogs.InvocationRunning, nil)
	run.write(workspacelogs.StreamStdout, "built\n")
	run.hook(workspacelogs.InvocationFailed, &exit)
	run.finish()
	ref := &fakeRefresher{reason: workspacelogs.ReasonSourceOffline}
	h := newHarness(s, setupLogsFlags{RunID: run.runID}, ref, false)

	err := h.reader.run(context.Background(), target())

	require.NoError(t, err, "a failed historical hook is data, not a command error")
	assert.Zero(t, ref.calls, "a completed run needs no provider access")
	assert.Contains(t, h.out.String(), "stdout | built\n")
	assert.Contains(t, h.out.String(), "# postCreateCommand: failed exit=3")
	assert.Empty(t, h.errOut.String())
}

func TestHistorySurvivesWorkspaceRemoval(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationSucceeded, nil)
	run.write(workspacelogs.StreamStdout, "kept\n")
	run.finish()
	// No workspace: no uid, no client, only the run ID.
	t0 := setupTarget{context: testCtx}
	h := newHarness(
		s,
		setupLogsFlags{RunID: run.runID},
		noRefresher{reason: reasonNoWorkspace},
		false,
	)

	require.NoError(t, h.reader.run(context.Background(), t0))
	assert.Contains(t, h.out.String(), "kept")
}

func TestRunOfAnotherWorkspaceIsRejected(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, "uid-old")
	run.finish()
	h := newHarness(s, setupLogsFlags{RunID: run.runID}, noRefresher{}, false)

	err := h.reader.run(context.Background(), target())

	require.ErrorContains(t, err, "another workspace")
	assert.Empty(t, h.out.String())
}

func TestRunOfAnotherContextIsRejected(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.finish()
	other := target()
	other.context = "elsewhere"
	h := newHarness(s, setupLogsFlags{RunID: run.runID}, noRefresher{}, false)

	require.ErrorContains(t, h.reader.run(context.Background(), other), "another context")
}

func TestLatestResolvesOnceToNewestRun(t *testing.T) {
	s := newStore(t)
	older := seedRun(t, s, testUID)
	older.hook(workspacelogs.InvocationSucceeded, nil)
	older.write(workspacelogs.StreamStdout, "old\n")
	older.finish()
	time.Sleep(1100 * time.Millisecond)
	newer := seedRun(t, s, testUID)
	newer.hook(workspacelogs.InvocationSucceeded, nil)
	newer.write(workspacelogs.StreamStdout, "new\n")
	newer.finish()
	h := newHarness(s, setupLogsFlags{}, noRefresher{}, false)

	require.NoError(t, h.reader.run(context.Background(), target()))
	assert.Contains(t, h.out.String(), "new")
	assert.NotContains(t, h.out.String(), "old")
}

func TestLatestWithoutRunsIsAnError(t *testing.T) {
	h := newHarness(newStore(t), setupLogsFlags{}, noRefresher{}, false)

	require.ErrorContains(t, h.reader.run(context.Background(), target()), "no retained setup runs")
}

func TestUnknownRunIsALookupError(t *testing.T) {
	h := newHarness(newStore(t), setupLogsFlags{RunID: workspacelogs.NewID()}, noRefresher{}, false)

	require.ErrorContains(t, h.reader.run(context.Background(), target()), "no retained setup run")
}

func TestNonexistentInvocationIsALookupError(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationSucceeded, nil)
	run.finish()
	h := newHarness(s, setupLogsFlags{RunID: run.runID, Invocation: workspacelogs.NewID()},
		noRefresher{}, false)

	err := h.reader.run(context.Background(), target())

	require.ErrorIs(t, err, workspacelogs.ErrNotFound)
	assert.Empty(t, h.out.String())
}

func TestSilentHookReturnsMetadataWithEmptyOutput(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	run.hook(workspacelogs.InvocationSucceeded, nil)
	run.finish()
	h := newHarness(s, setupLogsFlags{RunID: run.runID, Hook: hookCreate}, noRefresher{}, true)

	require.NoError(t, h.reader.run(context.Background(), target()))
	var resp workspacelogs.ReadResponse
	require.NoError(t, json.Unmarshal(h.out.Bytes(), &resp))
	assert.NotEmpty(t, resp.Records)
	for _, rec := range resp.Records {
		assert.NotEqual(t, workspacelogs.KindOutput, rec.Kind)
	}
}

func TestCursorBindsRunAndFilters(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	for range 6 {
		run.write(workspacelogs.StreamStdout, "line\n")
	}
	run.hook(workspacelogs.InvocationSucceeded, nil)
	run.finish()

	first := newHarness(s, setupLogsFlags{RunID: run.runID, Limit: 2}, noRefresher{}, true)
	require.NoError(t, first.reader.run(context.Background(), target()))
	var page workspacelogs.ReadResponse
	require.NoError(t, json.Unmarshal(first.out.Bytes(), &page))
	require.True(t, page.HasMore)

	resumed := newHarness(s, setupLogsFlags{RunID: run.runID, Limit: 2, After: page.NextCursor},
		noRefresher{}, true)
	require.NoError(t, resumed.reader.run(context.Background(), target()))

	changed := newHarness(s, setupLogsFlags{
		RunID: run.runID, Limit: 2, After: page.NextCursor, Hook: hookCreate,
	}, noRefresher{}, true)
	require.ErrorIs(t, changed.reader.run(context.Background(), target()),
		workspacelogs.ErrInvalidCursor)

	other := seedRun(t, s, testUID)
	other.finish()
	moved := newHarness(s, setupLogsFlags{RunID: other.runID, After: page.NextCursor},
		noRefresher{}, true)
	require.ErrorIs(t, moved.reader.run(context.Background(), target()),
		workspacelogs.ErrInvalidCursor)
}

func TestPlainTranscriptPagesPastFiveHundredRecords(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	const lines = 1300
	for i := range lines {
		run.write(workspacelogs.StreamStdout, fmt.Sprintf("line-%04d\n", i))
	}
	run.hook(workspacelogs.InvocationSucceeded, nil)
	run.finish()
	h := newHarness(s, setupLogsFlags{RunID: run.runID}, noRefresher{}, false)

	require.NoError(t, h.reader.run(context.Background(), target()))

	assert.Equal(t, lines, strings.Count(h.out.String(), "stdout | line-"))
	assert.Contains(t, h.out.String(), "line-0000")
	assert.Contains(t, h.out.String(), fmt.Sprintf("line-%04d", lines-1))
}

func TestPlainTranscriptKeepsStreamLabelsAndLiteralText(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	run.write(workspacelogs.StreamStdout, `{"kind":"status","x":1}`+"\n")
	run.write(workspacelogs.StreamStderr, "warn\n\n  indented")
	run.hook(workspacelogs.InvocationSucceeded, nil)
	run.finish()
	h := newHarness(s, setupLogsFlags{RunID: run.runID}, noRefresher{}, false)

	require.NoError(t, h.reader.run(context.Background(), target()))

	out := h.out.String()
	assert.Contains(t, out, `stdout | {"kind":"status","x":1}`+"\n")
	assert.Contains(t, out, "stderr | warn\n")
	assert.Contains(t, out, "stderr | \n")
	assert.Contains(t, out, "stderr |   indented")
}

func TestSecretNeverPresentInOutput(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	w := run.recorder.Writer(run.inv, workspacelogs.StreamStdout)
	_, _ = w.Write([]byte("token=" + testSecret[:7]))
	_, _ = w.Write([]byte(testSecret[7:] + "\n"))
	_ = w.Close()
	run.hook(workspacelogs.InvocationSucceeded, nil)
	run.finish()

	for _, jsonMode := range []bool{false, true} {
		h := newHarness(s, setupLogsFlags{RunID: run.runID}, noRefresher{}, jsonMode)
		require.NoError(t, h.reader.run(context.Background(), target()))
		assert.NotContains(t, h.out.String(), testSecret)
		assert.NotContains(t, h.errOut.String(), testSecret)
	}
}

func TestIncompleteRunReportsRefreshReasonOnStderr(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	run.write(workspacelogs.StreamStdout, "partial\n")
	ref := &fakeRefresher{reason: workspacelogs.ReasonRefreshUnsupported}
	h := newHarness(s, setupLogsFlags{RunID: run.runID}, ref, false)

	require.NoError(t, h.reader.run(context.Background(), target()), "unsupported is not a failure")

	assert.Equal(t, 1, ref.calls)
	assert.Contains(t, h.out.String(), "partial")
	assert.NotContains(t, h.out.String(), "unavailable")
	assert.Contains(t, h.errOut.String(), "unavailable: refresh_unsupported")
	assert.Contains(t, h.errOut.String(), "history is incomplete")
}

func TestIncompleteRunReportsRefreshReasonInJSON(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	ref := &fakeRefresher{reason: workspacelogs.ReasonSourceOffline}
	h := newHarness(s, setupLogsFlags{RunID: run.runID}, ref, true)

	require.NoError(t, h.reader.run(context.Background(), target()))

	var resp workspacelogs.ReadResponse
	require.NoError(t, json.Unmarshal(h.out.Bytes(), &resp))
	assert.False(t, resp.Complete)
	require.NotEmpty(t, resp.Availability)
	last := resp.Availability[len(resp.Availability)-1]
	assert.False(t, last.Available)
	assert.Equal(t, workspacelogs.ReasonSourceOffline, last.Reason)
	assert.Empty(t, h.errOut.String(), "JSON carries notices as metadata")
}

func TestFollowJSONIsNewlineDelimitedValidJSON(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	run.write(workspacelogs.StreamStdout, "first\n")
	ref := &fakeRefresher{}
	ref.onRefresh = func(call int) {
		if call == 1 {
			run.write(workspacelogs.StreamStdout, "second\n")
			run.hook(workspacelogs.InvocationSucceeded, nil)
			run.finish()
		}
	}
	h := newHarness(s, setupLogsFlags{RunID: run.runID, Follow: true}, ref, true)

	require.NoError(t, h.reader.run(context.Background(), target()))

	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	var all strings.Builder
	for _, line := range lines {
		var resp workspacelogs.ReadResponse
		require.NoError(t, json.Unmarshal([]byte(line), &resp), line)
		assert.LessOrEqual(t, len(line), workspacelogs.MaxPageBytes)
		for _, rec := range resp.Records {
			all.WriteString(rec.Text)
		}
	}
	assert.Equal(t, "first\nsecond\n", all.String())
	var last workspacelogs.ReadResponse
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last))
	assert.True(t, last.Complete)
	assert.Empty(t, h.errOut.String(), "no plain notices in JSON follow")
}

func TestFollowStopsWhenSourceIsOfflineWithNoReadPath(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	run.write(workspacelogs.StreamStdout, "so far\n")
	ref := &fakeRefresher{reason: workspacelogs.ReasonSourceOffline}
	h := newHarness(s, setupLogsFlags{RunID: run.runID, Follow: true}, ref, false)

	done := make(chan error, 1)
	go func() { done <- h.reader.run(context.Background(), target()) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("follow kept polling an offline source")
	}
	assert.Equal(t, 1, ref.calls)
	assert.Contains(t, h.out.String(), "so far")
	assert.Contains(t, h.errOut.String(), "unavailable: source_offline")
}

func TestFollowWaitsAtLeastMinimumBetweenPolls(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	var stamps []time.Time
	ref := &fakeRefresher{}
	ref.onRefresh = func(call int) {
		stamps = append(stamps, time.Now())
		if call == 3 {
			run.hook(workspacelogs.InvocationSucceeded, nil)
			run.finish()
		}
	}
	h := newHarness(s, setupLogsFlags{RunID: run.runID, Follow: true}, ref, false)

	require.NoError(t, h.reader.run(context.Background(), target()))

	require.Len(t, stamps, 3)
	for i := 1; i < len(stamps); i++ {
		assert.GreaterOrEqual(t, stamps[i].Sub(stamps[i-1]), minPollGap-20*time.Millisecond)
	}
}

func TestFollowStopsOnCancelWithoutError(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	ctx, cancel := context.WithCancel(context.Background())
	ref := &fakeRefresher{}
	ref.onRefresh = func(int) { cancel() }
	h := newHarness(s, setupLogsFlags{RunID: run.runID, Follow: true}, ref, false)

	require.NoError(t, h.reader.run(ctx, target()))
}

func TestFollowStopsAfterRepeatedRefreshFailures(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	ref := &fakeRefresher{reason: workspacelogs.ReasonRefreshFailed}
	h := newHarness(s, setupLogsFlags{RunID: run.runID, Follow: true}, ref, false)

	require.NoError(t, h.reader.run(context.Background(), target()))

	assert.Equal(t, maxRefreshFailures, ref.calls)
	assert.Contains(t, h.errOut.String(), "refresh_failed")
}

func TestListShowsRunsAndContinuationHint(t *testing.T) {
	s := newStore(t)
	for range 3 {
		seedRun(t, s, testUID).finish()
	}
	h := newHarness(s, setupLogsFlags{List: true, Limit: 2}, noRefresher{}, false)

	require.NoError(t, h.reader.run(context.Background(), target()))

	assert.Len(t, strings.Split(strings.TrimSpace(h.out.String()), "\n"), 2)
	assert.Contains(t, h.errOut.String(), "more runs: --after ")
}

func TestListWithoutWorkspaceIncludesAllRunsInContext(t *testing.T) {
	s := newStore(t)
	seedRun(t, s, "uid-a").finish()
	seedRun(t, s, "uid-b").finish()
	h := newHarness(s, setupLogsFlags{List: true}, noRefresher{}, true)

	require.NoError(t, h.reader.run(context.Background(), setupTarget{context: testCtx}))

	var resp workspacelogs.IndexResponse
	require.NoError(t, json.Unmarshal(h.out.Bytes(), &resp))
	assert.Len(t, resp.Runs, 2)
}

func TestNewRefresherChoosesNoReadPath(t *testing.T) {
	assert.Equal(t, reasonNoWorkspace,
		newRefresher(setupTarget{}).(noRefresher).reason)
	assert.Equal(t, workspacelogs.ReasonRefreshUnsupported,
		newRefresher(setupTarget{named: true}).(noRefresher).reason)
}

func TestClassifyRemoteFailure(t *testing.T) {
	assert.Equal(t, workspacelogs.ReasonSourceOffline,
		classifyRemoteFailure(workspacelogs.FormatUnavailable(workspacelogs.ReasonSourceOffline)))
	assert.Equal(t, workspacelogs.ReasonRefreshUnsupported,
		classifyRemoteFailure("Error: unknown flag: --source"))
	assert.Equal(t, workspacelogs.ReasonRefreshUnsupported,
		classifyRemoteFailure(`Error: unknown command "setup-logs"`))
	assert.Equal(t, workspacelogs.ReasonRefreshFailed, classifyRemoteFailure("boom"))
}

// fakeClient embeds the interface so any call other than the ones overridden
// panics: a refresh must only use Command.
type fakeClient struct {
	clientpkg.WorkspaceClient

	stdout   string
	stderr   string
	err      error
	commands []clientpkg.CommandOptions
	block    bool
}

func (f *fakeClient) AgentPath() string { return "/opt/devsy agent/devsy" }
func (f *fakeClient) Context() string   { return testCtx }
func (f *fakeClient) Workspace() string { return "ws" }

func (f *fakeClient) Command(ctx context.Context, o clientpkg.CommandOptions) error {
	f.commands = append(f.commands, o)
	if f.block {
		<-ctx.Done()

		return ctx.Err()
	}
	_, _ = o.Stdout.Write([]byte(f.stdout))
	_, _ = o.Stderr.Write([]byte(f.stderr))

	return f.err
}

// remoteResponse builds the JSON a container reader would print for a finished
// run that the local store does not have yet.
func remoteResponse(t *testing.T) (runID, body string) {
	t.Helper()
	remote := newStore(t)
	run := seedRun(t, remote, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	run.write(workspacelogs.StreamStdout, "from container\n")
	run.hook(workspacelogs.InvocationSucceeded, nil)
	run.finish()
	resp, err := remote.Read(workspacelogs.ReadRequest{RunID: run.runID})
	require.NoError(t, err)
	data, err := json.Marshal(resp)
	require.NoError(t, err)

	return run.runID, string(data)
}

func localMirror(t *testing.T, runID string) *workspacelogs.Store {
	t.Helper()
	s := newStore(t)
	_, err := s.CreateRun(workspacelogs.RunSpec{
		RunID:              runID,
		Context:            testCtx,
		WorkspaceUID:       testUID,
		WorkspaceIDAtStart: "ws",
		Command:            "up",
	})
	require.NoError(t, err)

	return s
}

func TestAgentRefresherImportsThroughReadOnlyCommand(t *testing.T) {
	runID, body := remoteResponse(t)
	local := localMirror(t, runID)
	client := &fakeClient{stdout: body}
	a := &agentRefresher{client: client, context: testCtx, uid: testUID}

	assert.Empty(t, a.refresh(context.Background(), local, runID))

	require.Len(t, client.commands, 1)
	cmd := client.commands[0]
	assert.True(t, cmd.RawStdout)
	assert.Nil(t, cmd.Stdin)
	assert.True(t, strings.HasPrefix(cmd.Command,
		"'/opt/devsy agent/devsy' internal agent workspace logs --source setup"))
	assert.Contains(t, cmd.Command, "--run "+runID)
	assert.Contains(t, cmd.Command, "--uid "+testUID)
	h := newHarness(local, setupLogsFlags{RunID: runID}, noRefresher{}, false)
	require.NoError(t, h.reader.run(context.Background(), target()))
	assert.Contains(t, h.out.String(), "from container")
}

func TestSetupReaderRefreshesIncompleteRunOnceThenReadsLocal(t *testing.T) {
	runID, body := remoteResponse(t)
	local := localMirror(t, runID)
	client := &fakeClient{stdout: body}
	h := newHarness(local, setupLogsFlags{RunID: runID},
		&agentRefresher{client: client, context: testCtx, uid: testUID}, false)

	require.NoError(t, h.reader.run(context.Background(), target()))
	require.NoError(t, h.reader.run(context.Background(), target()))

	assert.Len(t, client.commands, 1, "the second read finds a completed local run")
	assert.Equal(t, 2, strings.Count(h.out.String(), "from container"))
}

func TestAgentRefresherMapsFailuresToAvailability(t *testing.T) {
	tests := []struct {
		name   string
		client *fakeClient
		reason string
	}{
		{
			name:   "old agent",
			client: &fakeClient{err: errors.New("exit 1"), stderr: "Error: unknown flag: --source"},
			reason: workspacelogs.ReasonRefreshUnsupported,
		},
		{
			name: "offline",
			client: &fakeClient{
				err:    errors.New("exit 1"),
				stderr: workspacelogs.FormatUnavailable(workspacelogs.ReasonSourceOffline),
			},
			reason: workspacelogs.ReasonSourceOffline,
		},
		{
			name:   "invalid response",
			client: &fakeClient{stdout: "not json"},
			reason: workspacelogs.ReasonRefreshFailed,
		},
		{
			name:   "oversized response",
			client: &fakeClient{stdout: strings.Repeat(" ", workspacelogs.MaxPageBytes+10)},
			reason: workspacelogs.ReasonRefreshFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			run := seedRun(t, s, testUID)
			run.hook(workspacelogs.InvocationRunning, nil)
			a := &agentRefresher{client: tt.client, context: testCtx, uid: testUID}

			assert.Equal(t, tt.reason, a.refresh(context.Background(), s, run.runID))
			assert.Len(t, tt.client.commands, 1, "no retry and no fallback command")
		})
	}
}

func TestAgentRefresherRejectsResponseForAnotherWorkspace(t *testing.T) {
	runID, body := remoteResponse(t)
	local := localMirror(t, runID)
	a := &agentRefresher{client: &fakeClient{stdout: body}, context: testCtx, uid: "uid-other"}

	assert.Equal(
		t,
		workspacelogs.ReasonRefreshFailed,
		a.refresh(context.Background(), local, runID),
	)
	h := newHarness(local, setupLogsFlags{RunID: runID}, noRefresher{}, false)
	require.NoError(t, h.reader.run(context.Background(), target()))
	assert.NotContains(t, h.out.String(), "from container")
}

func TestAgentRefresherHonorsDeadline(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	a := &agentRefresher{client: &fakeClient{block: true}, context: testCtx, uid: testUID}
	start := time.Now()

	reason := a.refresh(context.Background(), s, run.runID)

	assert.Equal(t, workspacelogs.ReasonRefreshFailed, reason)
	assert.Less(t, time.Since(start), refreshDeadline+2*time.Second)
	assert.GreaterOrEqual(t, time.Since(start), refreshDeadline-100*time.Millisecond)
}

func TestFollowKeepsPollingLocalRunWithoutRefreshPath(t *testing.T) {
	s := newStore(t)
	run := seedRun(t, s, testUID)
	run.hook(workspacelogs.InvocationRunning, nil)
	ref := &fakeRefresher{reason: reasonNoWorkspace}
	ref.onRefresh = func(call int) {
		if call == 2 {
			run.write(workspacelogs.StreamStdout, "late line\n")
			run.hook(workspacelogs.InvocationSucceeded, nil)
			run.finish()
		}
	}
	h := newHarness(s, setupLogsFlags{RunID: run.runID, Follow: true}, ref, false)

	require.NoError(t, h.reader.run(context.Background(), target()))

	assert.GreaterOrEqual(t, ref.calls, 2)
	assert.Contains(t, h.out.String(), "late line")
}
