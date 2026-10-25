package agent

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/sharedfile"
)

const (
	// DefaultActivityInterval is the longest gap between activity touches.
	DefaultActivityInterval = 10 * time.Second

	activityFileMode = 0o666
	minTickInterval  = 10 * time.Millisecond
)

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
}

// Run blocks until ctx is done.
func (w ActivityWriter) Run(ctx context.Context) {
	w = w.withDefaults()

	var lastErr error
	record := func() {
		err := w.Touch(w.Path, w.Now())
		if (err == nil) != (lastErr == nil) {
			if err != nil {
				log.Errorf("activity writer: %v", err)
			} else {
				log.Infof("activity writer recovered")
			}
		}
		lastErr = err
	}

	ticks, stop := w.Tick(w.Interval)
	defer stop()
	record()
	for {
		select {
		case <-ctx.Done():
			record()
			return
		case <-ticks:
			record()
		}
	}
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
