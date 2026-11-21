package log

import (
	"context"
	"log/slog"

	"github.com/devsy-org/devsy/logging"
)

type contextKey struct{}

func Slog() *slog.Logger { return current.Load().logger }

func WithAttrs(attrs ...slog.Attr) *slog.Logger {
	return Slog().With(attrsToArgs(attrs)...)
}

func WithContext(ctx context.Context, attrs ...slog.Attr) context.Context {
	return context.WithValue(ctx, contextKey{}, FromContext(ctx).With(attrsToArgs(attrs)...))
}

func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if logger, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
			return logger
		}
	}
	return Slog()
}

func attrsToArgs(attrs []slog.Attr) []any {
	args := make([]any, len(attrs))
	for i := range attrs {
		args[i] = attrs[i]
	}
	return args
}

func logContext(ctx context.Context, level Level, msg string, attrs ...slog.Attr) {
	logger := FromContext(ctx)
	logger.LogAttrs(ctx, level, msg, attrs...)
}

func TraceContext(ctx context.Context, msg string, attrs ...slog.Attr) {
	logContext(ctx, logging.LevelTrace, msg, attrs...)
}

func DebugContext(ctx context.Context, msg string, attrs ...slog.Attr) {
	logContext(ctx, slog.LevelDebug, msg, attrs...)
}

func InfoContext(ctx context.Context, msg string, attrs ...slog.Attr) {
	logContext(ctx, slog.LevelInfo, msg, attrs...)
}

func WarnContext(ctx context.Context, msg string, attrs ...slog.Attr) {
	logContext(ctx, slog.LevelWarn, msg, attrs...)
}

func ErrorContext(ctx context.Context, msg string, attrs ...slog.Attr) {
	logContext(ctx, slog.LevelError, msg, attrs...)
}

func AttrWorkspaceID(v string) slog.Attr { return slog.String("workspace_id", v) }
func AttrMachineID(v string) slog.Attr   { return slog.String("machine_id", v) }
func AttrProvider(v string) slog.Attr    { return slog.String("provider", v) }
func AttrComponent(v string) slog.Attr   { return slog.String("component", v) }
func AttrOperation(v string) slog.Attr   { return slog.String("operation", v) }
func AttrPhase(v string) slog.Attr       { return slog.String("phase", v) }
func AttrDurationMs(v int64) slog.Attr   { return slog.Int64("duration_ms", v) }
func AttrErrorCode(v string) slog.Attr   { return slog.String("error_code", v) }
