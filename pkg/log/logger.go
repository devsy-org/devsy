package log

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/term"
)

// sugar is the package-level sugared logger. All package functions delegate to it.
var sugar *zap.SugaredLogger

func init() {
	// Default: silent until Init() is called. This avoids double-logging
	// during the migration period when the old logger is still active.
	sugar = zap.NewNop().Sugar()
}

// Config holds logger configuration parsed from CLI flags.
type Config struct {
	Verbosity int    // 0=error, 1=info+warn, 2=debug, 3=trace
	Quiet     bool   // fatal only
	Debug     bool   // backwards compat, equivalent to Verbosity=2
	Format    string // "text", "json", "logfmt", "raw"
}

// Init configures the global logger. Called once in root command PersistentPreRunE.
func Init(cfg Config) {
	level := resolveLevel(cfg)
	encoder := resolveEncoder(cfg.Format)
	core := zapcore.NewCore(encoder, zapcore.Lock(os.Stderr), level)

	var opts []zap.Option
	if cfg.Format != "raw" {
		opts = append(opts, zap.AddCaller(), zap.AddStacktrace(zapcore.FatalLevel))
	}
	logger := zap.New(core, opts...)
	sugar = logger.Sugar()
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
	case "raw":
		// Message only — no timestamp, level, or caller. Matches old MakeRaw().
		return zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
			MessageKey: "M",
		})
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
	return sugar.Desugar()
}

// Sync flushes any buffered log entries. Call before process exit.
func Sync() error {
	return sugar.Desugar().Sync()
}

// --- Package-level logging functions ---

func Debugf(format string, args ...any) { sugar.Debugf(format, args...) }
func Infof(format string, args ...any)  { sugar.Infof(format, args...) }
func Warnf(format string, args ...any)  { sugar.Warnf(format, args...) }
func Errorf(format string, args ...any) { sugar.Errorf(format, args...) }
func Fatalf(format string, args ...any) { sugar.Fatalf(format, args...) }

func Debug(args ...any) { sugar.Debug(args...) }
func Info(args ...any)  { sugar.Info(args...) }
func Warn(args ...any)  { sugar.Warn(args...) }
func Error(args ...any) { sugar.Error(args...) }
func Fatal(args ...any) { sugar.Fatal(args...) }

// Structured logging.
func Debugw(msg string, keysAndValues ...any) { sugar.Debugw(msg, keysAndValues...) }
func Infow(msg string, keysAndValues ...any)  { sugar.Infow(msg, keysAndValues...) }
func Warnw(msg string, keysAndValues ...any)  { sugar.Warnw(msg, keysAndValues...) }
func Errorw(msg string, keysAndValues ...any) { sugar.Errorw(msg, keysAndValues...) }
