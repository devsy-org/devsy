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
	DefaultActivityInterval = 10 * time.Second
	activityFileMode        = 0o666
	minTickInterval         = 10 * time.Millisecond
)

func TouchActivity(path string, now time.Time) error {
	if err := sharedfile.EnsureMode(path, activityFileMode); err != nil {
		return err
	}
	err := os.Chtimes(path, now, now)
	if errors.Is(err, os.ErrPermission) {
		return touchByWrite(path)
	}
	return err
}

func touchByWrite(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0) // #nosec G304 -- fixed activity file path
	if err != nil {
		return err
	}
	_, writeErr := f.WriteAt([]byte{0}, 0)
	return errors.Join(writeErr, f.Close())
}

// ActivityWriter retains file heartbeats for non-managed daemons. Managed work
// is protected by its acknowledged connection, including when all writes fail.
type ActivityWriter struct {
	Path         string
	LifecycleDir string
	RequireOwner bool
	Interval     time.Duration
	Touch        func(string, time.Time) error
	Now          func() time.Time
	Tick         func(time.Duration) (<-chan time.Time, func())
}

// Start returns only after registering with any live managed owner. The caller
// must use workCtx and stop its work if that context is cancelled.
func (w ActivityWriter) Start(ctx context.Context) (context.Context, func(), error) {
	client, err := w.connect(ctx)
	if err != nil {
		return nil, nil, err
	}
	if client != nil {
		return startRegisteredActivity(ctx, client)
	}
	w = w.withDefaults()
	workCtx, cancel := context.WithCancel(ctx)
	lastErr := w.record(nil)
	done := make(chan struct{})
	go func() { defer close(done); w.run(workCtx, lastErr) }()
	var once sync.Once
	return workCtx, func() { once.Do(func() { cancel(); <-done }) }, nil
}

func startRegisteredActivity(
	ctx context.Context,
	client *LifecycleClient,
) (context.Context, func(), error) {
	workCtx, cancel := client.ProtectedContext(ctx)
	if err := client.Send(LifecycleStateActivity, "", ""); err != nil {
		cancel(err)
		_ = client.Close()
		return nil, nil, err
	}
	// The live connection itself is the activity. No periodic file write or
	// health publication is needed, even when storage is completely unavailable.
	var once sync.Once
	return workCtx, func() {
		once.Do(func() {
			cancel(context.Canceled)
			if err := client.Send(LifecycleStateRelease, "", ""); err != nil {
				log.Debugf("activity release failed; closing its protection connection: %v", err)
			}
			_ = client.Close()
		})
	}, nil
}

// Run records legacy file activity without claiming managed protection. Managed
// callers use Start so registration completes before their work begins.
func (w ActivityWriter) Run(ctx context.Context) {
	w = w.withDefaults()
	w.run(ctx, w.record(nil))
}

func (w ActivityWriter) connect(ctx context.Context) (*LifecycleClient, error) {
	if w.LifecycleDir == "" {
		if w.RequireOwner {
			return nil, ErrLifecycleOwnerMissing
		}
		return nil, nil
	}
	owner, err := liveOwner(w.LifecycleDir, os.Geteuid())
	if err != nil {
		return nil, err
	}
	if owner != nil {
		return connectOwner(ctx, w.LifecycleDir, *owner)
	}
	if w.RequireOwner {
		return nil, ErrLifecycleOwnerMissing
	}
	// A discovery gap in an existing managed runtime is not a legacy workspace.
	if _, err := os.Lstat(w.LifecycleDir); !errors.Is(err, os.ErrNotExist) {
		return nil, ErrLifecycleOwnerMissing
	}
	return nil, nil
}

func (w ActivityWriter) run(ctx context.Context, lastErr error) {
	ticks, stop := w.Tick(w.Interval)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			_ = w.record(lastErr)
			return
		case <-ticks:
			lastErr = w.record(lastErr)
		}
	}
}

func (w ActivityWriter) record(lastErr error) error {
	err := w.Touch(w.Path, w.Now())
	if (err == nil) != (lastErr == nil) {
		if err != nil {
			log.Errorf("activity writer: %v", err)
		} else {
			log.Infof("activity writer recovered")
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
	return w
}

func realTick(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

func StartActivityWriter(
	ctx context.Context,
	interval time.Duration,
) (context.Context, func(), error) {
	required := false
	if encoded := os.Getenv(config.EnvWorkspaceDaemonConfig); encoded != "" {
		daemon, err := DecodeDaemonConfig(encoded)
		if err != nil {
			return nil, nil, err
		}
		required = daemon.Lifecycle.Managed()
	}
	return (ActivityWriter{
		Path: config.ContainerActivityFile, LifecycleDir: config.ContainerLifecycleDir,
		RequireOwner: required, Interval: interval,
	}).Start(ctx)
}
