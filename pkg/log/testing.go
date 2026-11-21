package log

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/internal/diagnosticredact"
	"github.com/devsy-org/devsy/logging"
	"github.com/devsy-org/devsy/logging/redact"
)

type LoggedEntry struct {
	Level   Level
	Message string
	Time    time.Time
	attrs   map[string]any
}

func (e LoggedEntry) AttrsMap() map[string]any {
	out := make(map[string]any, len(e.attrs))
	maps.Copy(out, e.attrs)
	return out
}

type observedStore struct {
	mu      sync.Mutex
	entries []LoggedEntry
}

type ObservedLogs struct {
	store      *observedStore
	predicates []func(LoggedEntry) bool
}

func (o *ObservedLogs) All() []LoggedEntry {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	return o.filtered(o.store.entries)
}

func (o *ObservedLogs) AllUntimed() []LoggedEntry {
	entries := o.All()
	for i := range entries {
		entries[i].Time = time.Time{}
	}
	return entries
}

func (o *ObservedLogs) TakeAll() []LoggedEntry {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	result := o.filtered(o.store.entries)
	remaining := make([]LoggedEntry, 0, len(o.store.entries)-len(result))
	for _, entry := range o.store.entries {
		if len(o.filtered([]LoggedEntry{entry})) == 0 {
			remaining = append(remaining, entry)
		}
	}
	o.store.entries = remaining
	return result
}
func (o *ObservedLogs) Len() int { return len(o.All()) }
func (o *ObservedLogs) FilterMessage(message string) *ObservedLogs {
	return o.filter(func(e LoggedEntry) bool { return e.Message == message })
}

func (o *ObservedLogs) FilterMessageSnippet(part string) *ObservedLogs {
	return o.filter(func(e LoggedEntry) bool { return strings.Contains(e.Message, part) })
}

func (o *ObservedLogs) FilterLevelExact(level Level) *ObservedLogs {
	return o.filter(func(e LoggedEntry) bool { return e.Level == level })
}

func (o *ObservedLogs) FilterAttr(attr slog.Attr) *ObservedLogs {
	return o.filter(
		func(e LoggedEntry) bool { return reflect.DeepEqual(e.attrs[attr.Key], attr.Value.Resolve().Any()) },
	)
}

func (o *ObservedLogs) filtered(entries []LoggedEntry) []LoggedEntry {
	out := make([]LoggedEntry, 0, len(entries))
	for _, e := range entries {
		match := true
		for _, predicate := range o.predicates {
			if !predicate(e) {
				match = false
				break
			}
		}
		if match {
			out = append(out, e)
		}
	}
	return out
}

func (o *ObservedLogs) filter(fn func(LoggedEntry) bool) *ObservedLogs {
	predicates := append([]func(LoggedEntry) bool(nil), o.predicates...)
	return &ObservedLogs{store: o.store, predicates: append(predicates, fn)}
}

type observedAttr struct {
	groups []string
	attr   slog.Attr
}
type observedHandler struct {
	store  *observedStore
	level  Level
	attrs  []observedAttr
	groups []string
}

func (h *observedHandler) Enabled(
	_ context.Context,
	level slog.Level,
) bool {
	return level >= h.level
}

func (h *observedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append([]observedAttr(nil), h.attrs...)
	for _, attr := range attrs {
		next.attrs = append(
			next.attrs,
			observedAttr{groups: append([]string(nil), h.groups...), attr: attr},
		)
	}
	return &next
}

func (h *observedHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.groups = append(append([]string(nil), h.groups...), name)
	return &next
}

func addObservedAttr(dst map[string]any, groups []string, attr slog.Attr) {
	for _, name := range groups {
		child, ok := dst[name].(map[string]any)
		if !ok {
			child = make(map[string]any)
			dst[name] = child
		}
		dst = child
	}
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		target := dst
		if attr.Key != "" {
			target = make(map[string]any)
			dst[attr.Key] = target
		}
		for _, item := range value.Group() {
			addObservedAttr(target, nil, item)
		}
	} else if attr.Key != "" {
		dst[attr.Key] = value.Any()
	}
}

func (h *observedHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any)
	for _, item := range h.attrs {
		addObservedAttr(attrs, item.groups, item.attr)
	}
	record.Attrs(func(attr slog.Attr) bool { addObservedAttr(attrs, h.groups, attr); return true })
	h.store.mu.Lock()
	h.store.entries = append(
		h.store.entries,
		LoggedEntry{Level: record.Level, Message: record.Message, Time: record.Time, attrs: attrs},
	)
	h.store.mu.Unlock()
	return nil
}

type testWriter struct{ t testing.TB }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}

func InitTest(t testing.TB) {
	t.Helper()
	redactor := redact.New(redact.Options{Environment: os.Environ()})
	writer := &redactingWriter{next: testWriter{t}, redactor: redactor}
	handler := logging.NewHandler(writer, logging.HandlerOptions{
		Level: slog.LevelDebug, Format: logging.FormatText, Redactor: redactor,
	})
	state := &loggerState{
		logger:   slog.New(diagnosticredact.ProtectMessages(handler, redactor)),
		level:    slog.LevelDebug,
		writer:   writer,
		redactor: redactor,
	}
	installTestState(t, state)
}

func InitTestObserved(t testing.TB, level Level) *ObservedLogs {
	t.Helper()
	store := &observedStore{}
	redactor := redact.New(redact.Options{Environment: os.Environ()})
	handler := logging.ProtectHandler(
		&observedHandler{store: store, level: level},
		redactor,
		logging.DropReserved,
	)
	state := &loggerState{
		logger:   slog.New(diagnosticredact.ProtectMessages(handler, redactor)),
		level:    level,
		redactor: redactor,
	}
	installTestState(t, state)
	return &ObservedLogs{store: store}
}

func installTestState(t testing.TB, state *loggerState) {
	stateMu.Lock()
	state.previous = current.Load()
	current.Store(state)
	stateMu.Unlock()
	t.Cleanup(func() {
		if state.writer != nil {
			_ = state.writer.Sync()
		}
		stateMu.Lock()
		defer stateMu.Unlock()
		state.removed = true
		if current.Load() != state {
			return
		}
		previous := state.previous
		for previous != nil && previous.removed {
			previous = previous.previous
		}
		if previous != nil {
			current.Store(previous)
		}
	})
}
