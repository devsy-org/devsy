package workspacelogs

import "context"

type recorderKey struct{}

// WithRecorder returns a context that carries rec so deep call sites can
// record without new parameters. A nil rec returns ctx unchanged.
func WithRecorder(ctx context.Context, rec *Recorder) context.Context {
	if rec == nil {
		return ctx
	}

	return context.WithValue(ctx, recorderKey{}, rec)
}

// RecorderFrom returns the recorder carried by ctx, or nil. Every Recorder
// method is safe to skip when this is nil.
func RecorderFrom(ctx context.Context) *Recorder {
	rec, _ := ctx.Value(recorderKey{}).(*Recorder)

	return rec
}

// RunID returns the run this recorder writes to.
func (r *Recorder) RunID() string { return r.opts.RunID }
