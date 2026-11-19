package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"

	"github.com/devsy-org/devsy/pkg/secrets"
)

type redactHandler struct {
	next     slog.Handler
	redactor *secrets.Redactor
	grouped  bool
}

func (h *redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, h.redactor.Redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool { clean.AddAttrs(h.cleanAttr(a, !h.grouped)); return true })
	return h.next.Handle(ctx, clean)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.cleanAttr(a, !h.grouped)
	}
	return &redactHandler{next: h.next.WithAttrs(clean), redactor: h.redactor, grouped: h.grouped}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{
		next:     h.next.WithGroup(h.redactor.Redact(name)),
		redactor: h.redactor,
		grouped:  h.grouped || name != "",
	}
}

func (h *redactHandler) cleanAttr(a slog.Attr, root bool) slog.Attr {
	a.Key = h.redactor.Redact(a.Key)
	if root && isEnvelopeKey(a.Key) {
		return slog.Attr{}
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(h.redactor.Redact(v.String()))
	case slog.KindGroup:
		a.Value = h.cleanGroup(v.Group(), root && a.Key == "")
	case slog.KindAny:
		a.Value = h.cleanAny(v, a.Value)
	default:
		a.Value = v
	}
	return a
}

func (h *redactHandler) cleanGroup(group []slog.Attr, root bool) slog.Value {
	clean := make([]slog.Attr, len(group))
	for i := range group {
		clean[i] = h.cleanAttr(group[i], root)
	}
	return slog.GroupValue(clean...)
}

func isEnvelopeKey(key string) bool {
	switch key {
	case "ts", slog.TimeKey, slog.MessageKey, "message", slog.LevelKey:
		return true
	}
	return false
}

func (h *redactHandler) cleanAny(v, original slog.Value) slog.Value {
	if err, ok := v.Any().(error); ok {
		return slog.StringValue(h.redactor.Redact(err.Error()))
	}
	data, err := json.Marshal(v.Any())
	if err != nil {
		return slog.StringValue(h.redactor.Redact(v.String()))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if dec.Decode(&value) == nil {
		return slog.AnyValue(h.cleanValue(value))
	}
	return original
}

func (h *redactHandler) cleanValue(v any) any {
	switch value := v.(type) {
	case string:
		return h.redactor.Redact(value)
	case []any:
		clean := make([]any, len(value))
		for i := range value {
			clean[i] = h.cleanValue(value[i])
		}
		return clean
	case map[string]any:
		clean := make(map[string]any, len(value))
		for k, v := range value {
			clean[h.redactor.Redact(k)] = h.cleanValue(v)
		}
		return clean
	}
	return v
}
