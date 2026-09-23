package machinediagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const currentSessionID = "current"

func TestStoreRecordsAndReadsEvents(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store, err := NewRecorder(
		Options{
			Dir:            filepath.Join(t.TempDir(), "diagnostics"),
			Reader:         ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()},
			PatrolInterval: time.Minute,
			Now:            func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	store.Record(Event{Type: EventDaemonStarted, Level: LevelInfo, Message: "token=secret-token"})
	store.Update(
		Status{StartedAt: now, State: DaemonRunning, Health: DaemonHealthy, PatrolInterval: "1m"},
	)

	response := Read(
		store.dir,
		ReadOptions{Limit: 10, Interval: time.Minute, Now: now.Add(time.Second)},
	)
	assert.Equal(t, AvailabilityAvailable, response.Availability)
	assert.Equal(t, FreshnessFresh, response.Freshness)
	require.Len(t, response.Events, 1)
	assert.Equal(t, uint64(1), response.Events[0].Sequence)
	assert.NotEmpty(t, response.Cursor.Next)
	assert.Equal(t, "token=secret-token", response.Events[0].Message)
}

func TestReadWithoutCursorReturnsMostRecentEvents(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	store, err := NewRecorder(Options{
		Dir:    filepath.Join(t.TempDir(), "diagnostics"),
		Reader: ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()},
		Now:    func() time.Time { return now },
	})
	require.NoError(t, err)
	for range 3 {
		store.Record(Event{Type: EventDaemonStarted, Level: LevelInfo, Message: "event"})
	}
	store.Update(Status{StartedAt: now, State: DaemonRunning, Health: DaemonHealthy})

	response := Read(store.dir, ReadOptions{Limit: 2, Interval: time.Minute, Now: now})
	require.Len(t, response.Events, 2)
	assert.Equal(t, uint64(2), response.Events[0].Sequence)
	assert.Equal(t, uint64(3), response.Events[1].Sequence)
}

func TestReadLocatorReportsPermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	path := filepath.Join(t.TempDir(), "locator.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o000))
	response := ReadFromLocator(
		path,
		ReadOptions{Limit: 10, Interval: time.Minute, Now: time.Now()},
	)
	assert.Equal(t, AvailabilityPermissionDenied, response.Availability)
	require.NotNil(t, response.Error)
	assert.Equal(t, "diagnostics_permission_denied", response.Error.Code)
}

func TestStoreRetentionGap(t *testing.T) {
	now := time.Now().UTC()
	store, err := NewRecorder(
		Options{
			Dir:             filepath.Join(t.TempDir(), "diagnostics"),
			Reader:          ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()},
			PatrolInterval:  time.Minute,
			MaxBytes:        100,
			MaxSegmentBytes: 60,
			Now:             func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	for range 10 {
		store.Record(
			Event{
				Type:    EventDaemonStarted,
				Level:   LevelInfo,
				Message: "a diagnostic event that requires retention rotation",
			},
		)
	}
	store.Update(
		Status{StartedAt: now, State: DaemonRunning, Health: DaemonHealthy, PatrolInterval: "1m"},
	)
	response := Read(
		store.dir,
		ReadOptions{
			After:    EncodeCursor(store.SessionID(), 0),
			Limit:    10,
			Interval: time.Minute,
			Now:      now,
		},
	)
	assert.Equal(t, CursorGap, response.Cursor.State)
	assert.NotEmpty(t, response.Events)
}

func TestSanitizerBoundsAndNormalizes(t *testing.T) {
	s := NewSanitizer([]string{"TOKEN=top-secret"})
	message := s.Message("top-secret\n" + string(make([]byte, MaxEventMessageBytes+20)))
	assert.NotContains(t, message, "top-secret")
	assert.NotContains(t, message, "\n")
	assert.LessOrEqual(t, len(message), MaxEventMessageBytes+len("…"))
}

func TestReadFreshness(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	status := &Status{UpdatedAt: now.Add(-4 * time.Minute)}
	assert.Equal(t, FreshnessStale, freshness(status, time.Minute, now))
	assert.Equal(t, FreshnessFresh, freshness(status, 10*time.Minute, now))
	status.PatrolInterval = "10m"
	assert.Equal(t, FreshnessFresh, freshness(status, time.Minute, now))
	status.PatrolInterval = "invalid"
	assert.Equal(t, FreshnessStale, freshness(status, time.Minute, now))
	assert.Equal(t, FreshnessUnknown, freshness(nil, time.Minute, now))
}

func TestLocatorRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "agent-daemon.json")
	want := Locator{
		SessionID:      "session",
		DiagnosticsDir: "/tmp/diagnostics",
		StartedAt:      time.Now().UTC(),
	}
	require.NoError(t, WriteLocator(path, want))
	got, err := ReadLocator(path)
	require.NoError(t, err)
	assert.Equal(t, SchemaVersion, got.SchemaVersion)
	assert.Equal(t, want.SessionID, got.SessionID)
	assert.Equal(t, want.DiagnosticsDir, got.DiagnosticsDir)
	want.SessionID = "replacement"
	require.NoError(t, WriteLocator(path, want))
	got, err = ReadLocator(path)
	require.NoError(t, err)
	assert.Equal(t, "replacement", got.SessionID)
}

func newTestDiagnosticsStore(t *testing.T, state DaemonState) string {
	return newTestDiagnosticsStoreAt(t, state, time.Now().UTC())
}

func newTestDiagnosticsStoreAt(t *testing.T, state DaemonState, updatedAt time.Time) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "diagnostics")
	store, err := NewRecorder(Options{
		Dir:    dir,
		Reader: ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()},
	})
	require.NoError(t, err)
	store.Update(Status{State: state, Health: DaemonHealthy, UpdatedAt: updatedAt})
	return dir
}

func writeTestLocator(t *testing.T, dir, diagnosticsDir string) string {
	t.Helper()
	path := filepath.Join(dir, "locator.json")
	require.NoError(t, WriteLocator(
		path,
		Locator{SessionID: dir, DiagnosticsDir: diagnosticsDir},
	))
	return path
}

func TestReadActiveLocatorCandidates(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	options := ReadOptions{Limit: 10, Interval: time.Minute, Now: now}

	t.Run("system wins", func(t *testing.T) {
		systemDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now)
		userDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now)
		system := writeTestLocator(t, t.TempDir(), systemDir)
		user := writeTestLocator(t, t.TempDir(), userDir)
		response := readActiveFromCandidates([]string{system, user}, options)
		systemResponse := Read(systemDir, options)
		userResponse := Read(userDir, options)
		require.NotNil(t, response.Status)
		assert.Equal(t, systemResponse.Status.SessionID, response.Status.SessionID)
		assert.NotEqual(t, userResponse.Status.SessionID, response.Status.SessionID)
	})

	t.Run("user fallback", func(t *testing.T) {
		userDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now)
		user := writeTestLocator(t, t.TempDir(), userDir)
		response := readActiveFromCandidates(
			[]string{filepath.Join(t.TempDir(), "missing"), user},
			options,
		)
		assert.Equal(t, DaemonRunning, response.Status.State)
	})

	t.Run("corrupt system is authoritative", func(t *testing.T) {
		system := filepath.Join(t.TempDir(), "system.json")
		require.NoError(t, os.WriteFile(system, []byte("{"), 0o600))
		userDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now)
		user := writeTestLocator(t, t.TempDir(), userDir)
		response := readActiveFromCandidates([]string{system, user}, options)
		assert.Equal(t, AvailabilityCorrupt, response.Availability)
	})

	t.Run("both missing", func(t *testing.T) {
		response := readActiveFromCandidates([]string{
			filepath.Join(t.TempDir(), "system.json"),
			filepath.Join(t.TempDir(), "user.json"),
		}, options)
		assert.Equal(t, AvailabilityNotInitialized, response.Availability)
	})

	if os.Getuid() != 0 {
		t.Run("permission system falls back", func(t *testing.T) {
			systemDir := t.TempDir()
			system := writeTestLocator(t, systemDir, newTestDiagnosticsStore(t, DaemonStopping))
			require.NoError(t, os.Chmod(system, 0o000))
			t.Cleanup(func() {
				_ = os.Chmod(system, 0o600) //nolint:gosec // restore test fixture access.
			})
			if _, err := ReadLocator(system); !os.IsPermission(err) {
				t.Skip("platform does not expose permission-denied file reads")
			}
			userDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now)
			user := writeTestLocator(t, t.TempDir(), userDir)
			response := readActiveFromCandidates([]string{system, user}, options)
			require.NotNil(t, response.Status)
			assert.Equal(t, DaemonRunning, response.Status.State)
		})
	}
}

func TestReadActiveStoppingSystemFallsBackToCurrentUser(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	options := ReadOptions{Limit: 10, Interval: time.Minute, Now: now}
	systemDir := newTestDiagnosticsStoreAt(t, DaemonStopping, now)
	userDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now)
	system := writeTestLocator(t, t.TempDir(), systemDir)
	user := writeTestLocator(t, t.TempDir(), userDir)

	response := readActiveFromCandidates([]string{system, user}, options)
	require.NotNil(t, response.Status)
	assert.Equal(t, DaemonRunning, response.Status.State)
}

func TestReadActiveStaleSystemFallsBackToCurrentUser(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	options := ReadOptions{Limit: 10, Interval: time.Minute, Now: now}
	systemDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now.Add(-5*time.Minute))
	userDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now)
	system := writeTestLocator(t, t.TempDir(), systemDir)
	user := writeTestLocator(t, t.TempDir(), userDir)

	response := readActiveFromCandidates([]string{system, user}, options)
	require.NotNil(t, response.Status)
	userResponse := Read(userDir, options)
	assert.Equal(t, userResponse.Status.SessionID, response.Status.SessionID)
	assert.Equal(t, FreshnessFresh, response.Freshness)
}

func TestReadActiveStaleSystemIsRetainedWithoutFreshFallback(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	options := ReadOptions{Limit: 10, Interval: time.Minute, Now: now}
	systemDir := newTestDiagnosticsStoreAt(t, DaemonRunning, now.Add(-5*time.Minute))
	system := writeTestLocator(t, t.TempDir(), systemDir)

	response := readActiveFromCandidates([]string{system}, options)
	require.NotNil(t, response.Status)
	assert.Equal(t, DaemonRunning, response.Status.State)
	assert.Equal(t, FreshnessStale, response.Freshness)
}

func TestRuntimeLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "daemon.lock")
	first, err := AcquireRuntimeLock(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	_, err = AcquireRuntimeLock(path)
	require.ErrorContains(t, err, "already running")
}

func TestValidateCursor(t *testing.T) {
	require.NoError(t, ValidateCursor(EncodeCursor("session", 1)))
	require.Error(t, ValidateCursor("not-a-cursor"))
}

func TestStatusAtomicityForConcurrentReaders(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	store, err := NewRecorder(
		Options{Dir: dir, Reader: ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()}},
	)
	require.NoError(t, err)
	var writers sync.WaitGroup
	writers.Go(func() {
		for range 50 {
			store.Update(Status{State: DaemonRunning, Health: DaemonHealthy})
		}
	})
	for range 50 {
		_, err := readStatus(filepath.Join(dir, "status.json"))
		if err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
	}
	writers.Wait()
}

func TestReaderIgnoresPartialFinalEvent(t *testing.T) {
	now := time.Now().UTC()
	store, err := NewRecorder(
		Options{
			Dir:    filepath.Join(t.TempDir(), "diagnostics"),
			Reader: ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()},
			Now:    func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	store.Record(Event{Type: EventDaemonStarted, Level: LevelInfo, Message: "started"})
	store.Update(Status{StartedAt: now, State: DaemonRunning, Health: DaemonHealthy})
	segments, err := store.segments()
	require.NoError(t, err)
	require.Len(t, segments, 1)
	f, err := os.OpenFile(segments[0], os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"schemaVersion":1`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	response := Read(store.dir, ReadOptions{Limit: 10, Interval: time.Minute, Now: now})
	require.Len(t, response.Events, 1)
}

func TestReaderReportsCompleteMalformedEventAsCorrupt(t *testing.T) {
	now := time.Now().UTC()
	store, err := NewRecorder(
		Options{
			Dir:    filepath.Join(t.TempDir(), "diagnostics"),
			Reader: ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()},
			Now:    func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	store.Record(Event{Type: EventDaemonStarted, Level: LevelInfo, Message: "started"})
	store.Update(Status{StartedAt: now, State: DaemonRunning, Health: DaemonHealthy})
	segments, err := store.segments()
	require.NoError(t, err)
	f, err := os.OpenFile(segments[0], os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("{not-json}\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	response := Read(store.dir, ReadOptions{Limit: 10, Interval: time.Minute, Now: now})
	assert.Equal(t, AvailabilityCorrupt, response.Availability)
	require.NotNil(t, response.Error)
	assert.Equal(t, "diagnostics_corrupt", response.Error.Code)
}

func TestReadCalculatesRetentionGapFromCurrentSessionOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	_, err := NewRecorder(
		Options{Dir: dir, Reader: ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()}},
	)
	require.NoError(t, err)
	status := Status{
		SchemaVersion: SchemaVersion,
		SessionID:     currentSessionID,
		UpdatedAt:     time.Now().UTC(),
	}
	statusBytes, err := json.Marshal(status)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status.json"), statusBytes, 0o600))
	oldEvent, err := json.Marshal(
		Event{
			SchemaVersion: SchemaVersion,
			SessionID:     "older",
			Sequence:      1,
			Timestamp:     time.Now().UTC(),
			Message:       "old",
		},
	)
	require.NoError(t, err)
	currentEvent, err := json.Marshal(
		Event{
			SchemaVersion: SchemaVersion,
			SessionID:     currentSessionID,
			Sequence:      5,
			Timestamp:     time.Now().UTC(),
			Message:       currentSessionID,
		},
	)
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "events", "a-old.ndjson"), append(oldEvent, '\n'), 0o600),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, "events", "z-current.ndjson"),
			append(currentEvent, '\n'),
			0o600,
		),
	)

	response := Read(
		dir,
		ReadOptions{
			After:    EncodeCursor(currentSessionID, 0),
			Limit:    10,
			Interval: time.Minute,
			Now:      time.Now(),
		},
	)
	assert.Equal(t, CursorGap, response.Cursor.State)
	require.Len(t, response.Events, 1)
	assert.Equal(t, uint64(5), response.Events[0].Sequence)
}

func TestReadRejectsStatusWithoutCurrentSchemaAndSession(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	_, err := NewRecorder(
		Options{Dir: dir, Reader: ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()}},
	)
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "status.json"), []byte(`{"schemaVersion":0}`), 0o600),
	)
	response := Read(dir, ReadOptions{Limit: 10, Interval: time.Minute, Now: time.Now()})
	assert.Equal(t, AvailabilityCorrupt, response.Availability)
}

func TestStoreRateLimitsWriteErrorReports(t *testing.T) {
	now := time.Now().UTC()
	var reports []error
	store, err := NewRecorder(Options{
		Dir: filepath.Join(
			t.TempDir(),
			"diagnostics",
		),
		Reader:        ReaderIdentity{UID: os.Getuid(), GID: os.Getgid()},
		Now:           func() time.Time { return now },
		ErrorReporter: func(err error) { reports = append(reports, err) },
	})
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(store.eventsDir))
	// A callback may inspect recorder state; it must not run under the mutex.
	store.errorReporter = func(err error) {
		store.mu.Lock()
		defer store.mu.Unlock()
		reports = append(reports, err)
	}
	store.Record(Event{Message: "first"})
	store.Record(Event{Message: "second"})
	require.Len(t, reports, 1)
}
