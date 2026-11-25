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
	value = redactor.RedactRegistered(value)
	if json.Valid([]byte(value)) {
		return wholeRecord(redactor, value)
	}
	return maskRecords(redactor, value)
}

func maskRecords(redactor *redact.Redactor, value string) string {
	var output strings.Builder
	for value != "" {
		candidate := strings.TrimLeft(value, " \t")
		if LooksLikeStructuredJSON(candidate[:min(len(candidate), maxStructuredRecordBytes)]) {
			indent := value[:len(value)-len(candidate)]
			consumed := maskStructuredRecord(redactor, indent, candidate, &output)
			if consumed > 0 {
				value = candidate[consumed:]
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
	switch value[0] {
	case '{':
		rest := strings.TrimLeft(value[1:], " \t\r\n")
		return rest == "" || rest[0] == '"' || rest[0] == '}'
	case '[':
		return looksLikeJSONList(strings.TrimLeft(value[1:], " \t\r\n"))
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

const (
	maxStructuredRecordBytes = 64 << 10
	maxStructuredRecordDepth = 10_000
)

func bracketedProse(line string) bool {
	if !strings.HasPrefix(line, "[") {
		return false
	}
	end := strings.IndexByte(line, ']')
	return end > 0 && !strings.ContainsAny(line[1:end], `"[{`)
}

func structuredRecordEnd(value string) int {
	if value[0] == '"' {
		return 1 + bytesThroughClosingQuote(value[1:])
	}
	return bytesThroughClosingBracket(value)
}

func bytesThroughClosingQuote(value string) int {
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(value)
}

func bytesThroughClosingBracket(value string) int {
	var opening [maxStructuredRecordDepth]byte
	opening[0] = value[0]
	depth := 1
	for i := 1; i < len(value); i++ {
		switch c := value[i]; c {
		case '"':
			i += bytesThroughClosingQuote(value[i+1:])
		case '[', '{':
			if depth == len(opening) {
				return len(value)
			}
			opening[depth] = c
			depth++
		case ']', '}':
			depth--
			if !matchingBrackets(opening[depth], c) {
				return len(value)
			}
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(value)
}

func matchingBrackets(opening, closing byte) bool {
	return opening == '[' && closing == ']' || opening == '{' && closing == '}'
}

func malformedRecordLength(value, prefix string, err error) int {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return structuredRecordEnd(value)
	}
	line, _, _ := splitDiagnosticRecord(prefix)
	if strings.HasPrefix(line, `"`) || bracketedProse(line) {
		return 0
	}
	var syntax *json.SyntaxError
	if len(line) == len(prefix) && len(prefix) < len(value) ||
		(errors.As(err, &syntax) && syntax.Offset > int64(len(line))) {
		return structuredRecordEnd(value)
	}
	return len(line)
}

func maskStructuredRecord(
	redactor *redact.Redactor,
	indent, value string,
	output *strings.Builder,
) int {
	limit := min(len(value), maxStructuredRecordBytes)
	prefix := value[:limit]
	decoder := json.NewDecoder(strings.NewReader(prefix))
	var record json.RawMessage
	if err := decoder.Decode(&record); err != nil {
		consumed := malformedRecordLength(value, prefix, err)
		if consumed > 0 {
			output.WriteString(redactor.Mask())
		}
		return consumed
	}
	end := int(decoder.InputOffset())
	output.WriteString(indent)
	output.WriteString(wholeRecord(redactor, string(record)))
	return end
}
