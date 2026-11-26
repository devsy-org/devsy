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
