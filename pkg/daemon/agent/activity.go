package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/sharedfile"
)

const (
	// failureMarkerInfix separates the activity file path from the pid of a writer
	// that currently cannot record activity.
	failureMarkerInfix = ".failing."

	// DefaultActivityInterval is the longest gap between activity touches.
	DefaultActivityInterval = 10 * time.Second

	activityFileMode = 0o666
	minTickInterval  = 10 * time.Millisecond
)

var writerSequence atomic.Uint64

// HeartbeatInterval is how often activity is recorded for a given timeout: at
// most the default cadence, and at most a third of short timeouts.
func HeartbeatInterval(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultActivityInterval
	}
	return min(DefaultActivityInterval, max(timeout/3, minTickInterval))
}

// TouchActivity records activity at now, creating the shared file if needed.
func TouchActivity(path string, now time.Time) error {
	if err := sharedfile.EnsureMode(path, activityFileMode); err != nil {
		return err
	}
	err := os.Chtimes(path, now, now)
	if errors.Is(err, os.ErrPermission) {
		// Setting an explicit time needs ownership; any writer may bump mtime.
		return touchByWrite(path)
	}
	return err
}

// touchByWrite updates the modification time through a write, which the
// 0666 activity file permits for every user.
func touchByWrite(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0) // #nosec G304 -- fixed activity file path
	if err != nil {
		return err
	}
	_, writeErr := f.WriteAt([]byte{0}, 0)
	return errors.Join(writeErr, f.Close())
}

// ActivityWriter records activity immediately, periodically, and once more when
// its context ends. Zero-value hooks use the real clock and file.
type ActivityWriter struct {
	Path     string
	Interval time.Duration
	Touch    func(path string, now time.Time) error
	Now      func() time.Time
	// Tick returns a tick channel and its stop function.
	Tick func(d time.Duration) (<-chan time.Time, func())
	// Report publishes whether this writer currently fails to record activity.
	// It defaults to a failure marker next to the activity file.
	Report func(path string, failed bool) error
}

// Run blocks until ctx is done.
func (w ActivityWriter) Run(ctx context.Context) {
	w = w.withDefaults()

	var lastErr error
	// A writer that exits has no recording left to vouch for or against.
	defer func() { _ = w.Report(w.Path, false) }()

	ticks, stop := w.Tick(w.Interval)
	defer stop()
	lastErr = w.record(lastErr)
	for {
		select {
		case <-ctx.Done():
			w.record(lastErr)
			return
		case <-ticks:
			lastErr = w.record(lastErr)
		}
	}
}

// record touches the activity file and publishes the resulting health. It
// returns the touch error so the caller can detect transitions.
func (w ActivityWriter) record(lastErr error) error {
	err := w.Touch(w.Path, w.Now())
	if (err == nil) != (lastErr == nil) {
		if err != nil {
			log.Errorf("activity writer: %v", err)
		} else {
			log.Infof("activity writer recovered")
		}
	}
	// Failures are re-reported each tick so a marker that could not be written
	// once is retried.
	if err != nil || lastErr != nil {
		if reportErr := w.Report(w.Path, err != nil); reportErr != nil {
			log.Errorf("activity writer: publish recording health: %v", reportErr)
		}
	}
	return err
}

func (w ActivityWriter) withDefaults() ActivityWriter {
	if w.Touch == nil {
		w.Touch = TouchActivity
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.Tick == nil {
		w.Tick = realTick
	}
	if w.Interval <= 0 {
		w.Interval = DefaultActivityInterval
	}
	if w.Report == nil {
		w.Report = recordingFailureReporter(w.Path)
	}
	return w
}

func realTick(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// StartActivityWriter runs an ActivityWriter on the shared activity file until
// the returned stop function is called; stop waits for the final touch.
func StartActivityWriter(ctx context.Context, interval time.Duration) (stop func()) {
	wctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		ActivityWriter{Path: config.ContainerActivityFile, Interval: interval}.Run(wctx)
	})
	return func() {
		cancel()
		wg.Wait()
	}
}

// failureMarkerPath prefixes markers with the writer's PID so the owner can
// discard reports left by exited writers.
func failureMarkerPath(activityPath string, pid int) string {
	return activityPath + failureMarkerInfix + strconv.Itoa(pid)
}

// recordingFailureReporter isolates each writer's health, including concurrent
// connections in one process.
func recordingFailureReporter(activityPath string) func(string, bool) error {
	marker := failureMarkerPath(activityPath, os.Getpid()) + "." +
		strconv.FormatUint(writerSequence.Add(1), 10)
	return func(_ string, failed bool) error {
		if !failed {
			if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		f, err := openNoFollow(marker, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, activityFileMode)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(procStartTicks(os.Getpid()))
		return errors.Join(writeErr, f.Close())
	}
}

// ActivityRecordingFailing reports whether any live writer has published a
// failure marker for activityPath. Markers of exited writers are removed.
func ActivityRecordingFailing(activityPath string) bool {
	dir, base := filepath.Split(activityPath)
	entries, err := os.ReadDir(filepath.Clean(dir))
	if err != nil {
		return true
	}
	prefix := base + failureMarkerInfix
	failing := false
	for _, entry := range entries {
		writer, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok {
			continue
		}
		marker := filepath.Join(dir, entry.Name())
		pidText, _, _ := strings.Cut(writer, ".")
		if markerWriterAlive(marker, pidText) {
			failing = true
		} else {
			_ = os.Remove(marker)
		}
	}
	return failing
}

func markerWriterAlive(marker, pidText string) bool {
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return false
	}
	if !processExists(pid) {
		return false
	}
	ticks, err := sharedfile.ReadFile(marker)
	if err != nil {
		return true
	}
	return sameProcess(pid, string(ticks))
}
