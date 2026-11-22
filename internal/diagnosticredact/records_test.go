package diagnosticredact

import (
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
)

func TestRecordsHandleManyBlankRecordsInLinearTime(t *testing.T) {
	input := strings.Repeat("\n", 100_000) + "tail"
	start := time.Now()
	output := Records(redact.FromEntries(nil), input)
	assert.Equal(t, input, output)
	assert.Less(t, time.Since(start), time.Second)
}

func TestRecordsKeepPlainBracketedTextUnmasked(t *testing.T) {
	redactor := redact.FromEntries(nil)
	for _, input := range []string{
		"[1, 2 workers] ready\npublic tail",
		"[INFO] ready\n[1, 2 workers] ready\npublic tail",
		"{not json} ready\npublic tail",
	} {
		assert.Equal(t, input, Records(redactor, input), input)
	}
}

func TestRecordsMaskUnterminatedJSONRecordThroughItsEnd(t *testing.T) {
	const canary = "synthetic-unterminated-canary"
	redactor := redact.FromEntries(nil)
	input := "public head\n{\n  \"Cookie\": \"" + canary + "\"\n"
	output := Records(redactor, input)
	assert.NotContains(t, output, canary)
	assert.Contains(t, output, "public head\n")
}

func TestRecordsMaskOversizedStructuredRecordsWithoutExposingTheirTail(t *testing.T) {
	const canary = "synthetic-oversized-cookie"
	redactor := redact.FromEntries(nil)
	head := "{\"details\":[\"" + strings.Repeat("x", maxStructuredRecordBytes) + `\"}]","next"],`
	input := "public head\n" + head + "\n" + `"Cookie":"` + canary + `"`
	assert.Equal(t, "public head\n"+redactor.Mask(), Records(redactor, input))
	assert.Equal(t, "public head\n"+redactor.Mask()+"\r\npublic tail",
		Records(redactor, input+"}\r\npublic tail"))
}

func TestRecordsMaskStructuredArraysThatStartWithScalars(t *testing.T) {
	redactor := redact.FromEntries(nil)
	const canary = "synthetic-array-cookie"
	input := "public head\n[1,\n{\"Cookie\":\"" + canary + "\"}]\npublic tail"
	output := Records(redactor, input)
	assert.NotContains(t, output, canary)
	assert.Contains(t, output, "public head\n[1,")
	assert.Contains(t, output, "\"Cookie\":\"***\"")
	assert.Contains(t, output, "]\npublic tail")
}

func TestRecordsMaskMalformedJSONOnlyThroughTheLine(t *testing.T) {
	const canary = "synthetic-malformed-canary"
	redactor := redact.FromEntries(nil)
	output := Records(redactor, "{\"a\": "+canary+"\npublic tail")
	assert.NotContains(t, output, canary)
	assert.Contains(t, output, "\npublic tail")
}
