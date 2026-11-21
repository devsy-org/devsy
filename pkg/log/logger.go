package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/internal/diagnosticredact"
	"github.com/devsy-org/devsy/logging"
	"github.com/devsy-org/devsy/logging/redact"
	"golang.org/x/term"
)

type loggerState struct {
	logger   *slog.Logger
	level    Level
	writer   *redactingWriter
	redactor *redact.Redactor
	previous *loggerState
	removed  bool
}

var (
	current    atomic.Pointer[loggerState]
	stateMu    sync.Mutex
	extraSinks = &writerFanout{}
)

func init() { Init(Config{}) }

type Config struct {
	Verbosity    int
	Quiet        bool
	Debug        bool
	VerbositySet bool
	Level        string
	DefaultLevel string
	Format       string
	Redactor     *redact.Redactor
}

func Init(cfg Config) {
	redactor := redact.Combine(cfg.Redactor, redact.New(redact.Options{Environment: os.Environ()}))
	level := resolveLevel(cfg)
	writer := &redactingWriter{next: outputWriter{}}
	format := logging.FormatText
	style := logging.TextUppercase
	switch cfg.Format {
	case "json":
		format = logging.FormatJSON
	case "logfmt":
		format = logging.FormatLogfmt
	default:
		if colorEnabled() {
			style = logging.TextColored
		}
	}
	handler := logging.NewHandler(writer, logging.HandlerOptions{
		Level: level, Format: format, TextStyle: style, Redactor: redactor,
	})
	stateMu.Lock()
	if old := current.Load(); old != nil && old.writer != nil {
		_ = old.writer.Sync()
	}
	current.Store(
		&loggerState{
			logger:   slog.New(diagnosticredact.ProtectMessages(handler, redactor)),
			level:    level,
			writer:   writer,
			redactor: redactor,
		},
	)
	stateMu.Unlock()
}

type outputWriter struct{}

func (outputWriter) Write(p []byte) (int, error) {
	consoleOutput.RLock()
	if consoleOutput.suppressed == 0 {
		_, _ = os.Stderr.Write(p)
	}
	consoleOutput.RUnlock()
	return extraSinks.Write(p)
}

var consoleOutput = struct {
	sync.RWMutex
	suppressed int
}{}

func enabled(level Level) bool { return level >= current.Load().level }

func SuppressConsoleOutput() func() {
	consoleOutput.Lock()
	consoleOutput.suppressed++
	consoleOutput.Unlock()
	var once sync.Once
	return func() { once.Do(func() { consoleOutput.Lock(); consoleOutput.suppressed--; consoleOutput.Unlock() }) }
}

type redactingWriter struct {
	mu       sync.Mutex
	next     io.Writer
	redactor *redact.Redactor
	stream   *redact.Stream
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	if w == nil || w.next == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stream == nil {
		w.stream = diagnosticredact.Stream(w.redactor)
	}
	redacted := []byte(w.stream.RedactChunk(string(p)))
	if len(redacted) == 0 {
		return len(p), nil
	}
	_, err := w.next.Write(redacted)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *redactingWriter) Sync() error {
	if w == nil || w.next == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stream != nil {
		if pending := w.stream.Flush(); pending != "" {
			if _, err := w.next.Write([]byte(pending)); err != nil {
				return err
			}
		}
	}
	if syncer, ok := w.next.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	return nil
}

// AddSink attaches w as an additional destination for log output for the
// duration of the returned remove func. Each line of log output is written to
// w in addition to stderr. Multiple concurrent sinks are supported.
//
// w must be safe for concurrent Write calls - AddSink does not serialize
// writes to a single sink. io.Pipe writers and io.MultiWriter wrapping
// pre-serialized destinations are both fine; bare bytes.Buffer is not.
func AddSink(w io.Writer) (remove func()) {
	return extraSinks.add(w)
}

type sinkRegistration struct {
	writer io.Writer
}

type writerFanout struct {
	mu      sync.RWMutex
	writers []*sinkRegistration
}

func (f *writerFanout) Write(p []byte) (int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var result error
	for _, registration := range f.writers {
		n, err := registration.writer.Write(p)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		if err != nil {
			result = errors.Join(result, err)
		}
	}
	if result != nil {
		return 0, result
	}
	return len(p), nil
}

func (*writerFanout) Sync() error { return nil }

func (f *writerFanout) add(w io.Writer) (remove func()) {
	registration := &sinkRegistration{writer: w}
	f.mu.Lock()
	f.writers = append(f.writers, registration)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, x := range f.writers {
			if x == registration {
				f.writers = append(f.writers[:i], f.writers[i+1:]...)
				return
			}
		}
	}
}

func resolveLevel(cfg Config) Level {
	switch {
	case cfg.Quiet:
		return slog.LevelError
	case cfg.Debug:
		return slog.LevelDebug
	case cfg.VerbositySet || cfg.Verbosity > 0:
		return VerbosityToLevel(cfg.Verbosity)
	default:
		return resolveNamedLevel(cfg)
	}
}

func resolveNamedLevel(cfg Config) Level {
	name, fallback := cfg.DefaultLevel, slog.LevelWarn
	if cfg.Level != "" {
		name, fallback = cfg.Level, VerbosityToLevel(cfg.Verbosity)
	}
	level, ok := LevelFromString(name)
	if ok {
		return level
	}
	return fallback
}

func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stderr.Fd()))
}

func Sync() error {
	if writer := current.Load().writer; writer != nil {
		return writer.Sync()
	}
	return nil
}

func ForwardFlagArgs() []string {
	level := LevelString()
	if level == DefaultLevel {
		return nil
	}
	return []string{"--log-level", level}
}

func emit(level Level, message string) {
	if enabled(level) {
		Slog().Log(context.Background(), level, message)
	}
}

func emitf(level Level, format string, args ...any) {
	if enabled(level) {
		emit(level, fmt.Sprintf(format, args...))
	}
}

func Tracef(format string, args ...any) { emitf(logging.LevelTrace, format, args...) }
func Debugf(format string, args ...any) { emitf(slog.LevelDebug, format, args...) }
func Infof(format string, args ...any)  { emitf(slog.LevelInfo, format, args...) }
func Warnf(format string, args ...any)  { emitf(slog.LevelWarn, format, args...) }
func Errorf(format string, args ...any) { emitf(slog.LevelError, format, args...) }
func Fatalf(format string, args ...any) {
	emitf(logging.LevelFatal, format, args...)
	_ = Sync()
	os.Exit(1)
}

func Trace(args ...any) {
	if enabled(logging.LevelTrace) {
		emit(logging.LevelTrace, fmt.Sprint(args...))
	}
}

func Debug(args ...any) {
	if enabled(slog.LevelDebug) {
		emit(slog.LevelDebug, fmt.Sprint(args...))
	}
}

func Info(args ...any) {
	if enabled(slog.LevelInfo) {
		emit(slog.LevelInfo, fmt.Sprint(args...))
	}
}

func Warn(args ...any) {
	if enabled(slog.LevelWarn) {
		emit(slog.LevelWarn, fmt.Sprint(args...))
	}
}

func Error(args ...any) {
	if enabled(slog.LevelError) {
		emit(slog.LevelError, fmt.Sprint(args...))
	}
}
func Fatal(args ...any) { emit(logging.LevelFatal, fmt.Sprint(args...)); _ = Sync(); os.Exit(1) }

func emitRecord(level Level, timestamp time.Time, message string, attrs []slog.Attr) {
	logger := Slog()
	if !logger.Enabled(context.Background(), level) {
		return
	}
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	record := slog.NewRecord(timestamp, level, message, 0)
	record.AddAttrs(attrs...)
	_ = logger.Handler().Handle(context.Background(), record)
}

func Debugw(msg string, args ...any) { Slog().Debug(msg, args...) }
func Infow(msg string, args ...any)  { Slog().Info(msg, args...) }
func Warnw(msg string, args ...any)  { Slog().Warn(msg, args...) }
func Errorw(msg string, args ...any) { Slog().Error(msg, args...) }
