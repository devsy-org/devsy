package up

import (
	"testing"

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

func TestFixtureDiagnosticMasksRecordShapes(t *testing.T) {
	for _, tc := range reportShapes {
		t.Run(tc.name, func(t *testing.T) {
			got := fixtureDiagnostic(nil, tc.in)
			assert.NotContains(t, got, "CANARY")
			assert.Contains(t, got, tc.public)
		})
	}
}
