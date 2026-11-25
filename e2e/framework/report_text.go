package framework

import (
	"github.com/devsy-org/devsy/e2e/framework/recordmask"
	"github.com/devsy-org/devsy/logging/redact"
)

// ReportText masks value for failure reports, including JSON records mixed
// with log lines.
func ReportText(redactor *redact.Redactor, value string) string {
	return recordmask.ReportText(redactor, value)
}
