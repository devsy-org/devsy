package framework

import (
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
)

func TestReportTextMasksRecordShapesRawRecordScanMisses(t *testing.T) {
	cases := map[string]struct {
		in     string
		public string
	}{
		"two JSON documents": {
			in:     "{\"Cookie\":\"CANARY\"}\n{\"message\":\"public detail\"}",
			public: "public detail",
		},
		"pretty JSON between log lines": {
			in:     "public log\n{\n  \"Cookie\": \"CANARY\"\n}\npublic tail",
			public: "public tail",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := ReportText(redact.New(redact.Options{}), tc.in)
			assert.NotContains(t, got, "CANARY")
			assert.Contains(t, got, tc.public)
		})
	}
}
