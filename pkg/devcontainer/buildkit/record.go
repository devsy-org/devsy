package buildkit

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

const buildPhase = "build"

// Step records one build step as an invocation in the run found in the
// context. A nil Step does nothing, so callers need no recorder checks.
type Step struct {
	rec     *workspacelogs.Recorder
	id      string
	started time.Time
	writers []io.Closer
}

// StartStep marks a build step running. It returns nil when no run is being
// recorded.
func StartStep(ctx context.Context, key string) *Step {
	rec := workspacelogs.RecorderFrom(ctx)
	if rec == nil {
		return nil
	}
	s := &Step{rec: rec, id: workspacelogs.NewID(), started: time.Now()}
	rec.Invocation(workspacelogs.Invocation{
		ID: s.id, Phase: buildPhase, Key: key, State: workspacelogs.InvocationRunning,
	})

	return s
}

// RecordSkipped records a build step that was not needed, with the reason.
func RecordSkipped(ctx context.Context, key, reason string) {
	rec := workspacelogs.RecorderFrom(ctx)
	if rec == nil {
		return
	}
	rec.Invocation(workspacelogs.Invocation{
		ID: workspacelogs.NewID(), Phase: buildPhase, Key: key,
		State: workspacelogs.InvocationSkipped, Reason: reason,
	})
}

// Tee returns a writer that records redacted output of the stream before
// forwarding to dst. The recorder comes first so the text is kept even when
// dst filters or fails. Without a step it returns dst unchanged.
func (s *Step) Tee(dst io.Writer, stream workspacelogs.Stream) io.Writer {
	if s == nil {
		return dst
	}
	w := s.rec.Writer(s.id, stream)
	s.writers = append(s.writers, w)

	return io.MultiWriter(w, dst)
}

// Finish flushes every writer, then records the terminal state for err.
func (s *Step) Finish(ctx context.Context, err error) {
	if s == nil {
		return
	}
	for _, w := range s.writers {
		_ = w.Close()
	}
	s.writers = nil
	inv := workspacelogs.Invocation{
		ID: s.id, Phase: buildPhase, State: workspacelogs.InvocationSucceeded,
		Duration: time.Since(s.started),
	}
	if err != nil {
		inv.State = failureState(ctx, err)
		inv.ExitCode = exitCode(err)
	}
	s.rec.Invocation(inv)
}

func failureState(ctx context.Context, err error) workspacelogs.InvocationState {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return workspacelogs.InvocationCanceled
	}

	return workspacelogs.InvocationFailed
}

func exitCode(err error) *int {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return nil
	}
	code := exitErr.ExitCode()

	return &code
}
