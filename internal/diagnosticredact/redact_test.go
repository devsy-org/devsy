package diagnosticredact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordKeepsConservativeURLMasking(t *testing.T) {
	redactor := redact.FromEntries(nil)
	const privateURL = "https://user:synthetic/path@host.test/"
	const publicMessage = "ordinary client detail"
	const diagnostic = "Authorization:\n Bearer synthetic-folded\n\n" + publicMessage
	assert.Equal(t, "https://***@host.test/", Record(redactor, privateURL))
	context := strings.Repeat("a", 17*1024)
	raw, err := json.Marshal(map[string]string{
		"msg": diagnostic, "url": privateURL, "level": "error", "context": context,
	})
	require.NoError(t, err)
	var cleaned map[string]string
	require.NoError(t, json.Unmarshal([]byte(Record(redactor, string(raw))), &cleaned))
	assert.Equal(t, "error", cleaned["level"])
	assert.Equal(t, context, cleaned["context"])
	assert.Equal(t, redactor.Mask(), cleaned["url"])
	assert.Equal(t, redactor.Mask(), cleaned["msg"])
}

func TestRecordKeepsCompleteInputMaskingAcrossMixedRecords(t *testing.T) {
	const canary = "synthetic-mixed-record-value"
	const detail = "ordinary mixed record detail"
	input := "\"Authorization:\"\n Bearer " + canary + "\n\n" + detail
	output := Record(redact.FromEntries(nil), input)
	assert.NotContains(t, output, canary)
	assert.Contains(t, output, detail)
}

func TestRecordKeepsOrdinaryPrefixAtTerminalFold(t *testing.T) {
	const canary = "synthetic-terminal-fold-value"
	const detail = "ordinary detail before terminal fold"
	input := detail + "\nAuthorization: Bearer synthetic-initial-value\n " + canary
	output := Record(redact.FromEntries(nil), input)
	assert.NotContains(t, output, canary)
	assert.Contains(t, output, detail)
}

func TestEnvironmentRecordsMaskJSONRecordsAfterPlainLines(t *testing.T) {
	const canary = "synthetic-record-bearer"
	for name, separator := range map[string]string{"LF": "\n", "CR": "\r", "CRLF": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			input := "public diagnostic" + separator +
				`{"Authorization":"Bearer ` + canary + `"}` + separator + "public tail"
			for label, output := range map[string]string{
				"EnvironmentRecord":       EnvironmentRecord(input),
				"RecordWithEnvironment":   RecordWithEnvironment(nil, input),
				"RecordsThenRecord":       Record(redact.FromEntries(nil), Records(redact.FromEntries(nil), input)),
				"RecordsWithOnlyPlainEOL": Records(redact.FromEntries(nil), "plain"+separator+"text"),
			} {
				assert.NotContains(t, output, canary, label)
			}
			output := RecordWithEnvironment(nil, input)
			assert.Contains(t, output, "public diagnostic"+separator)
			assert.Contains(t, output, separator+"public tail")
		})
	}
}

func TestRecordMasksJSONCredentialRecordsBeforePublicText(t *testing.T) {
	const canary = "synthetic-cookie-record"
	for name, separator := range map[string]string{"LF": "\n", "CR": "\r", "CRLF": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			input := `{"Cookie":"` + canary + `"}` + separator + "public text"
			output := Record(redact.FromEntries(nil), input)
			assert.NotContains(t, output, canary)
			assert.Contains(t, output, separator+"public text")
		})
	}
}

func TestRecordsMaskRegisteredMultilineValueBeforeSplittingRecords(t *testing.T) {
	const cookie = "synthetic-registered-cookie"
	const tail = "synthetic registered plain tail"
	registered := `{"Cookie":"` + cookie + `"}` + "\n" + tail
	redactor := redact.New(redact.Options{
		Environment: []string{"DEVSY_FIXTURE_TOKEN=" + registered},
	})
	input := "public before\n" + registered + "\npublic after"
	for name, mask := range map[string]func(*redact.Redactor, string) string{
		"Record":  Record,
		"Records": Records,
	} {
		t.Run(name, func(t *testing.T) {
			output := mask(redactor, input)
			assert.NotContains(t, output, cookie)
			assert.NotContains(t, output, tail)
			assert.Contains(t, output, "public before")
			assert.Contains(t, output, "public after")
		})
	}
}
