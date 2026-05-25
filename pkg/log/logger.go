package log

import (
	"os"
	"sync/atomic"

	cliErrors "github.com/devsy-org/devsy/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/term"
)

var sugar atomic.Pointer[zap.SugaredLogger]

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
	core := zapcore.NewCore(encoder, zapcore.Lock(os.Stderr), level)

	opts := []zap.Option{
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.FatalLevel),
	}
	logger := zap.New(core, opts...)
	sugar.Store(logger.Sugar())
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
func JSONError(cliErr *cliErrors.CLIError) {
	if cliErr == nil {
		return
	}
	sugar.Load().Desugar().Error(cliErr.Message, zap.Any("cliError", cliErr))
}

func Debugw(msg string, keysAndValues ...any) { sugar.Load().Debugw(msg, keysAndValues...) }
func Infow(msg string, keysAndValues ...any)  { sugar.Load().Infow(msg, keysAndValues...) }
func Warnw(msg string, keysAndValues ...any)  { sugar.Load().Warnw(msg, keysAndValues...) }
func Errorw(msg string, keysAndValues ...any) { sugar.Load().Errorw(msg, keysAndValues...) }
