package log

import "github.com/go-logr/logr"

func LogrSink() logr.LogSink {
	return logr.FromSlogHandler(Slog().Handler()).GetSink()
}
