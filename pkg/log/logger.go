package log

import (
	"io"
	"os"
	"sync"
	"sync/atomic"

	cliErrors "github.com/devsy-org/devsy/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/term"
)

var (
	sugar atomic.Pointer[zap.SugaredLogger]
	// extraSinks fans every log line out to writers added by AddSink. The
	// stderr core remains the primary destination; sinks are additive.
	extraSinks = &writerFanout{}
)

func init() {
	sugar.Store(zap.NewNop().Sugar())
}

// Config holds logger configuration parsed from CLI flags.
type Config struct {
	Verbosity int    // 0=error, 1=info+warn, 2=debug, 3=trace
	Quiet     bool   // fatal only
	Debug     bool   // backwards compat, equivalent to Verbosity=2
	Format    string // "text", "json", "logfmt"
}

// Init configures the global logger. Called once in root command PersistentPreRunE.
func Init(cfg Config) {
	level := resolveLevel(cfg)
	encoder := resolveEncoder(cfg.Format)
	stderrCore := zapcore.NewCore(encoder, zapcore.Lock(os.Stderr), level)
	// A separate core writes to the fanout sink with the same level/encoder
	// so AddSink consumers see the same output stderr does.
	sinkCore := zapcore.NewCore(resolveEncoder(cfg.Format), extraSinks, level)
	core := zapcore.NewTee(stderrCore, sinkCore)

	logger := zap.New(core, zap.AddStacktrace(zapcore.FatalLevel))
	sugar.Store(logger.Sugar())
}

// AddSink attaches w as an additional destination for log output for the
// duration of the returned remove func. Each line of log output is written to
// w in addition to stderr. Multiple concurrent sinks are supported.
//
// w must be safe for concurrent Write calls — AddSink does not serialize
// writes to a single sink. io.Pipe writers and io.MultiWriter wrapping
// pre-serialized destinations are both fine; bare bytes.Buffer is not.
func AddSink(w io.Writer) (remove func()) {
	return extraSinks.add(w)
}

// writerFanout is a zapcore.WriteSyncer that dispatches every Write to a
// dynamic set of writers. Safe for concurrent use.
type writerFanout struct {
	mu      sync.RWMutex
	writers []io.Writer
}

func (f *writerFanout) Write(p []byte) (int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, w := range f.writers {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

func (*writerFanout) Sync() error { return nil }

func (f *writerFanout) add(w io.Writer) (remove func()) {
	f.mu.Lock()
	f.writers = append(f.writers, w)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, x := range f.writers {
			if x == w {
				f.writers = append(f.writers[:i], f.writers[i+1:]...)
				return
			}
		}
	}
}

func resolveLevel(cfg Config) zapcore.Level {
	if cfg.Quiet {
		return zapcore.FatalLevel
	}
	if cfg.Debug {
		return zapcore.DebugLevel
	}
	return VerbosityToLevel(cfg.Verbosity)
}

func resolveEncoder(format string) zapcore.Encoder {
	switch format {
	case "json":
		return zapcore.NewJSONEncoder(jsonEncoderConfig())
	case "logfmt":
		return newLogfmtEncoder()
	default:
		// "text" — use console encoder, with color if stderr is a terminal
		if term.IsTerminal(int(os.Stderr.Fd())) { //nolint:gosec // fd fits in int
			return zapcore.NewConsoleEncoder(colorEncoderConfig())
		}
		return zapcore.NewConsoleEncoder(plainEncoderConfig())
	}
}

func jsonEncoderConfig() zapcore.EncoderConfig {
	cfg := zap.NewProductionEncoderConfig()
	cfg.TimeKey = "ts"
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	return cfg
}

func colorEncoderConfig() zapcore.EncoderConfig {
	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	return cfg
}

func plainEncoderConfig() zapcore.EncoderConfig {
	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.EncodeLevel = zapcore.CapitalLevelEncoder
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	return cfg
}

// Underlying returns the raw *zap.Logger for advanced use cases.
func Underlying() *zap.Logger {
	return sugar.Load().Desugar()
}

// Sync flushes any buffered log entries. Call before process exit.
func Sync() error {
	return sugar.Load().Desugar().Sync()
}

// --- Package-level logging functions ---

func Debugf(format string, args ...any) { sugar.Load().Debugf(format, args...) }
func Infof(format string, args ...any)  { sugar.Load().Infof(format, args...) }
func Warnf(format string, args ...any)  { sugar.Load().Warnf(format, args...) }
func Errorf(format string, args ...any) { sugar.Load().Errorf(format, args...) }
func Fatalf(format string, args ...any) { sugar.Load().Fatalf(format, args...) }

func Debug(args ...any) { sugar.Load().Debug(args...) }
func Info(args ...any)  { sugar.Load().Info(args...) }
func Warn(args ...any)  { sugar.Load().Warn(args...) }
func Error(args ...any) { sugar.Load().Error(args...) }
func Fatal(args ...any) { sugar.Load().Fatal(args...) }

// JSONError writes a single structured zap entry carrying a *CLIError under
// the "cliError" field. The desktop IPC layer parses this field by name.
//
// The top-level "msg" is set to the original error chain (when available) so
// that log consumers grepping the textual message still see the underlying
// cause. The friendly, user-facing summary remains in cliError.message and is
// what the desktop UI surfaces.
func JSONError(cliErr *cliErrors.CLIError) {
	if cliErr == nil {
		return
	}
	msg := cliErr.Message
	if wrapped := cliErr.Unwrap(); wrapped != nil {
		if s := wrapped.Error(); s != "" {
			msg = s
		}
	} else if cliErr.Cause != "" {
		msg = cliErr.Cause
	}
	sugar.Load().Desugar().Error(msg, zap.Object("cliError", cliErr))
}

func Debugw(msg string, keysAndValues ...any) { sugar.Load().Debugw(msg, keysAndValues...) }
func Infow(msg string, keysAndValues ...any)  { sugar.Load().Infow(msg, keysAndValues...) }
func Warnw(msg string, keysAndValues ...any)  { sugar.Load().Warnw(msg, keysAndValues...) }
func Errorw(msg string, keysAndValues ...any) { sugar.Load().Errorw(msg, keysAndValues...) }
