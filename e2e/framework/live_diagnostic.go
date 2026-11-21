package framework

import (
	"io"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/livetee"
)

type liveDiagnosticCapture struct {
	tee    *livetee.Tee
	masker *recordMasker
}

func newLiveDiagnosticCapture(tee *livetee.Tee, redactor *redact.Redactor) *liveDiagnosticCapture {
	return &liveDiagnosticCapture{
		tee: tee,
		masker: newRecordMasker(redactor, func(text string) {
			_, _ = io.WriteString(tee, text)
		}),
	}
}

func (w *liveDiagnosticCapture) Write(p []byte) (int, error) {
	w.masker.write(string(p))
	return len(p), nil
}

func (w *liveDiagnosticCapture) close() int64 {
	w.masker.finish()
	return w.tee.Close()
}
