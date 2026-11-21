package diagnosticredact

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"

	"github.com/devsy-org/devsy/logging/redact"
)

func EnvironmentRecord(value string) string {
	return RecordWithEnvironment(os.Environ(), value)
}

func RecordWithEnvironment(environment []string, value string) string {
	return Record(redact.New(redact.Options{Environment: environment}), value)
}

func Record(redactor *redact.Redactor, value string) string {
	if redactor == nil {
		return value
	}
	conservative := redactor.Redact(value)
	folded := redactor.RedactDiagnostic(value)
	switch {
	case conservative == value || conservative == folded:
		return folded
	case folded == value:
		return conservative
	case json.Valid([]byte(value)):
		return reconcileObjects(redactor, conservative, folded)
	default:
		return reconcileText(redactor, conservative, folded)
	}
}

func reconcileText(redactor *redact.Redactor, left, right string) string {
	prefix, suffix := sharedEdges(left, right)
	start, end := left[:prefix], left[len(left)-suffix:]
	marker := redactor.Mask()
	if strings.HasSuffix(start, marker) || strings.HasPrefix(end, marker) {
		marker = ""
	}
	masked := strings.ToValidUTF8(start+marker+end, "\uFFFD")
	if completeMaskIsStable(redactor, masked) {
		return masked
	}
	return redactor.Mask()
}

func sharedEdges(left, right string) (int, int) {
	limit := min(len(left), len(right))
	prefix := 0
	for prefix < limit && left[prefix] == right[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < limit-prefix && left[len(left)-1-suffix] == right[len(right)-1-suffix] {
		suffix++
	}
	return prefix, suffix
}

func reconcileObjects(redactor *redact.Redactor, conservative, folded string) string {
	mask, _ := json.Marshal(redactor.Mask())
	left, right := decodeObject(conservative), decodeObject(folded)
	if left == nil || right == nil {
		return string(mask)
	}
	for key, value := range left {
		next, present := right[key]
		if !present {
			delete(left, key)
		} else if !bytes.Equal(value, next) {
			left[key] = mask
		}
	}
	merged, err := json.Marshal(left)
	if err != nil || !completeMaskIsStable(redactor, string(merged)) {
		return string(mask)
	}
	return string(merged)
}

func completeMaskIsStable(redactor *redact.Redactor, value string) bool {
	return redactor.Redact(value) == value && redactor.RedactDiagnostic(value) == value
}

func decodeObject(value string) map[string]json.RawMessage {
	var result map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &result) != nil {
		return nil
	}
	return result
}
