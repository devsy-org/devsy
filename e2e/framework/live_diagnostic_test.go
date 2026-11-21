package framework

import (
	"bytes"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/livetee"
	"github.com/stretchr/testify/assert"
)

func liveOutput(input string, size int) string {
	var live bytes.Buffer
	capture := newLiveDiagnosticCapture(
		livetee.New(&bytes.Buffer{}, &live),
		redact.New(redact.Options{}),
	)
	for start := 0; start < len(input); start += size {
		end := min(start+size, len(input))
		_, _ = capture.Write([]byte(input[start:end]))
	}
	capture.close()
	return live.String()
}

func TestLiveDiagnosticCaptureMasksRecordsIndependentlyOfChunking(t *testing.T) {
	cases := map[string]struct {
		input  string
		canary string
		public []string
	}{
		"pretty object": {
			input: "[INFO] start\n{\n  \"Cookie\": \"chunk-cookie-fixture\",\n" +
				"  \"note\": \"public detail\"\n}\nprompt: \n" +
				"{\"Cookie\":\"chunk-compact-fixture\"}\ntail",
			canary: "chunk-cookie-fixture",
			public: []string{"[INFO] start\n", "public detail", "prompt: \n", "tail"},
		},
		"compact second record": {
			input:  "{\"Cookie\":\"chunk-compact-fixture\"}\n",
			canary: "chunk-compact-fixture",
		},
		"array opening with an element": {
			input:  "[{\n  \"Cookie\": \"chunk-array-fixture\"\n}]\npublic array tail\n",
			canary: "chunk-array-fixture",
			public: []string{"public array tail\n"},
		},
		"final fragment without newline": {
			input:  "{\n  \"note\": \"public final detail\",\n  \"Cookie\": \"chunk-final-fixture\"\n}",
			canary: "chunk-final-fixture",
			public: []string{"public final detail"},
		},
		"carriage return separated": {
			input:  "progress\r{\"Cookie\":\"chunk-cr-fixture\"}\rpublic cr tail\r",
			canary: "chunk-cr-fixture",
			public: []string{"progress\r", "public cr tail\r"},
		},
		"indented record": {
			input:  "  {\n    \"Cookie\": \"chunk-indent-fixture\"\n  }\npublic indent tail\n",
			canary: "chunk-indent-fixture",
			public: []string{"public indent tail\n"},
		},
		"bracketed log prefix": {
			input:  "[INFO] ready\n[WARN] public warning\n",
			public: []string{"[INFO] ready\n", "[WARN] public warning\n"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			whole := liveOutput(tc.input, len(tc.input))
			if tc.canary != "" {
				assert.NotContains(t, whole, tc.canary)
			}
			for _, public := range tc.public {
				assert.Contains(t, whole, public)
			}
			for _, size := range []int{1, 2, 3, 7, 16} {
				assert.Equal(t, whole, liveOutput(tc.input, size), "chunk size %d", size)
			}
		})
	}
}

func TestLiveDiagnosticCaptureMasksUnterminatedRecordAtClose(t *testing.T) {
	out := liveOutput("{\n  \"Cookie\": \"chunk-open-fixture\"\n", 5)
	assert.NotContains(t, out, "chunk-open-fixture")
	assert.Contains(t, out, "***")
}

func TestExternalCommandDiagnosticMasksEmbeddedJSONRecords(t *testing.T) {
	const input = "public log line\n{\n  \"Cookie\": \"external-cookie-fixture\",\n" +
		"  \"note\": \"public json detail\"\n}\npublic tail"
	out := externalCommandDiagnostic(input)
	assert.NotContains(t, out, "external-cookie-fixture")
	assert.Contains(t, out, "public log line")
	assert.Contains(t, out, "public json detail")
	assert.Contains(t, out, "public tail")
}
