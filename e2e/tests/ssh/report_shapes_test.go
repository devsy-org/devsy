package ssh

import (
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
)

var reportShapes = []struct{ name, in, public string }{
	{
		"two JSON documents",
		"{\"Cookie\":\"CANARY\"}\n{\"message\":\"public detail\"}",
		"public detail",
	},
	{
		"pretty JSON between log lines",
		"public log\n{\n  \"Cookie\": \"CANARY\"\n}\npublic tail",
		"public tail",
	},
}

func TestSSHReportHelpersMaskRecordShapes(t *testing.T) {
	for _, tc := range reportShapes {
		t.Run(tc.name, func(t *testing.T) {
			redactor := redact.New(redact.Options{})
			for label, got := range map[string]string{
				"sshDiagnostic":       sshDiagnostic(tc.in),
				"proxyCommandExcerpt": proxyCommandExcerpt(redactor, tc.in),
			} {
				assert.NotContains(t, got, "CANARY", label)
				assert.Contains(t, got, tc.public, label)
			}
		})
	}
}
