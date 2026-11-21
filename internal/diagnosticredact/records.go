package diagnosticredact

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/devsy-org/devsy/logging/redact"
)

func Records(redactor *redact.Redactor, value string) string {
	if redactor == nil {
		return value
	}
	if json.Valid([]byte(value)) {
		return wholeRecord(redactor, value)
	}
	var output strings.Builder
	for value != "" {
		if LooksLikeStructuredJSON(value) {
			if consumed := maskStructuredRecord(redactor, value, &output); consumed > 0 {
				value = value[consumed:]
				continue
			}
		}
		record, separator, rest := splitDiagnosticRecord(value)
		if json.Valid([]byte(record)) {
			record = wholeRecord(redactor, record)
		}
		output.WriteString(record)
		output.WriteString(separator)
		value = rest
	}
	return output.String()
}

func splitDiagnosticRecord(value string) (record, separator, rest string) {
	end := strings.IndexAny(value, "\r\n")
	if end < 0 {
		return value, "", ""
	}
	next := end + 1
	if strings.HasPrefix(value[end:], "\r\n") {
		next++
	}
	return value[:end], value[end:next], value[next:]
}

func LooksLikeStructuredJSON(value string) bool {
	value = strings.TrimLeft(value, " \t")
	if value == "" {
		return false
	}
	rest := strings.TrimLeft(value[1:], " \t\r\n")
	switch value[0] {
	case '{':
		return rest == "" || rest[0] == '"' || rest[0] == '}'
	case '[':
		return looksLikeJSONList(rest)
	case '"':
		return true
	default:
		return false
	}
}

func looksLikeJSONList(rest string) bool {
	if rest == "" || strings.ContainsRune(`{[]"`, rune(rest[0])) {
		return true
	}
	if !strings.ContainsRune(`-0123456789tfn`, rune(rest[0])) {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(rest))
	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		return errors.Is(err, io.ErrUnexpectedEOF)
	}
	tail := strings.TrimLeft(rest[int(decoder.InputOffset()):], " \t\r\n")
	return tail == "" || tail[0] == ',' || tail[0] == ']'
}

func maskStructuredRecord(redactor *redact.Redactor, value string, output *strings.Builder) int {
	decoder := json.NewDecoder(strings.NewReader(value))
	var record json.RawMessage
	if err := decoder.Decode(&record); err != nil {
		if strings.HasPrefix(strings.TrimLeft(value, " \t"), `"`) {
			return 0
		}
		output.WriteString(redactor.Mask())
		return len(value)
	}
	end := int(decoder.InputOffset())
	output.WriteString(value[:end-len(record)])
	output.WriteString(wholeRecord(redactor, string(record)))
	return end
}
