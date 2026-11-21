package diagnosticredact

import (
	"context"
	"log/slog"

	"github.com/devsy-org/devsy/logging/redact"
)

type messageHandler struct {
	slog.Handler
	redactor *redact.Redactor
}

func ProtectMessages(next slog.Handler, redactor *redact.Redactor) slog.Handler {
	if redactor == nil {
		return next
	}
	return messageHandler{Handler: next, redactor: redactor}
}

func (h messageHandler) Handle(ctx context.Context, record slog.Record) error {
	record.Message = Record(h.redactor, record.Message)
	return h.Handler.Handle(ctx, record)
}

func (h messageHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.Handler = h.Handler.WithAttrs(attrs)
	return h
}

func (h messageHandler) WithGroup(name string) slog.Handler {
	h.Handler = h.Handler.WithGroup(name)
	return h
}
