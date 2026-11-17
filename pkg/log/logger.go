package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
	"golang.org/x/term"
)

type loggerState struct {
	logger   *slog.Logger
	level    Level
	writer   *redactingWriter
	redactor *secrets.Redactor
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
	Level        string
	DefaultLevel string
	Format       string
	Redactor     *secrets.Redactor
}

func Init(cfg Config) {
	redactor := secrets.Combine(cfg.Redactor, secrets.NewEnvironmentRedactor(os.Environ()))
	level := resolveLevel(cfg)
	writer := &redactingWriter{next: outputWriter{}, redactor: redactor}
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: replaceEnvelope}
	switch cfg.Format {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	case "logfmt":
		handler = slog.NewTextHandler(writer, opts)
	default:
		color := colorEnabled()
		opts.ReplaceAttr = func(groups []string, attr slog.Attr) slog.Attr {
			if level, ok := attr.Value.Any().(slog.Level); len(groups) == 0 &&
				attr.Key == slog.LevelKey &&
				ok {
				name := strings.ToUpper(levelName(level))
				if color {
					code := "36"
					if level >= SlogLevelError {
						code = "31"
					} else if level >= SlogLevelWarn {
						code = "33"
					}
					name = "\x1b[" + code + "m" + name + "\x1b[0m"
				}
				return slog.String("level", name)
			}
			return replaceEnvelope(groups, attr)
		}
		handler = slog.NewTextHandler(writer, opts)
	}
	handler = &redactHandler{next: handler, redactor: redactor}
	stateMu.Lock()
	if old := current.Load(); old != nil && old.writer != nil {
		_ = old.writer.Sync()
	}
	current.Store(
		&loggerState{logger: slog.New(handler), level: level, writer: writer, redactor: redactor},
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

func replaceEnvelope(groups []string, a slog.Attr) slog.Attr {
	if len(groups) != 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		if a.Value.Kind() == slog.KindTime {
			return slog.String("ts", a.Value.Time().Format("2006-01-02T15:04:05.000Z0700"))
		}
	case slog.LevelKey:
		if level, ok := a.Value.Any().(slog.Level); ok {
			return slog.String("level", levelName(level))
		}
	}
	return a
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
	redactor *secrets.Redactor
	stream   *secrets.StreamingRedactor
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	if w == nil || w.next == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stream == nil {
		w.stream = secrets.NewStreamingRedactor(w.redactor)
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
	value := cfg.Level
	if value == "" {
		value = cfg.DefaultLevel
	}
	if level, ok := LevelFromString(value); ok {
		return level
	}
	return SlogLevelWarn
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

func ForwardFlagArgs() []string { return []string{"--log-level", LevelString()} }

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

func Tracef(format string, args ...any) { emitf(SlogLevelTrace, format, args...) }
func Debugf(format string, args ...any) { emitf(SlogLevelDebug, format, args...) }
func Infof(format string, args ...any)  { emitf(SlogLevelInfo, format, args...) }
func Warnf(format string, args ...any)  { emitf(SlogLevelWarn, format, args...) }
func Errorf(format string, args ...any) { emitf(SlogLevelError, format, args...) }
func Fatalf(format string, args ...any) {
	emitf(SlogLevelFatal, format, args...)
	_ = Sync()
	os.Exit(1)
}

func Trace(args ...any) {
	if enabled(SlogLevelTrace) {
		emit(SlogLevelTrace, fmt.Sprint(args...))
	}
}

func Debug(args ...any) {
	if enabled(SlogLevelDebug) {
		emit(SlogLevelDebug, fmt.Sprint(args...))
	}
}

func Info(args ...any) {
	if enabled(SlogLevelInfo) {
		emit(SlogLevelInfo, fmt.Sprint(args...))
	}
}

func Warn(args ...any) {
	if enabled(SlogLevelWarn) {
		emit(SlogLevelWarn, fmt.Sprint(args...))
	}
}

func Error(args ...any) {
	if enabled(SlogLevelError) {
		emit(SlogLevelError, fmt.Sprint(args...))
	}
}
func Fatal(args ...any) { emit(SlogLevelFatal, fmt.Sprint(args...)); _ = Sync(); os.Exit(1) }

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
