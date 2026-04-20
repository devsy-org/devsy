package log

import (
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
)

// LogrSink returns a logr.LogSink for Kubernetes client-go compatibility.
func LogrSink() logr.LogSink {
	return zapr.NewLogger(Underlying()).GetSink()
}
