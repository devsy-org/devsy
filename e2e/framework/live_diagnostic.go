package framework

import (
	"io"

	"github.com/devsy-org/devsy/e2e/framework/recordmask"
	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/livetee"
)

type liveDiagnosticCapture struct {
	tee    *livetee.Tee
	masker *recordmask.Masker
}

func newLiveDiagnosticCapture(tee *livetee.Tee, redactor *redact.Redactor) *liveDiagnosticCapture {
	return &liveDiagnosticCapture{
		tee: tee,
		masker: recordmask.New(redactor, func(text string) {
			_, _ = io.WriteString(tee, text)
		}),
	}
}

func (w *liveDiagnosticCapture) Write(p []byte) (int, error) {
	w.masker.Write(string(p))
	return len(p), nil
}

func (w *liveDiagnosticCapture) close() int64 {
	w.masker.Finish()
	return w.tee.Close()
}
