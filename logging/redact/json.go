package redact

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

func (r *Redactor) redactJSON(text string) (string, bool) {
	if !json.Valid([]byte(text)) {
		return "", false
	}
	if r.literalCrossesJSONValues(text) {
		return r.jsonMask(), true
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return r.jsonMask(), true
	}
	result, err := json.Marshal(r.SanitizeJSON(value))
	if err != nil || r.ContainsValue(string(result)) {
		return r.jsonMask(), true
	}
	return string(result), true
}

func (r *Redactor) jsonMask() string {
	encoded, _ := json.Marshal(r.Mask())
	return string(encoded)
}

func (r *Redactor) SanitizeJSON(value any) any {
	switch value := value.(type) {
	case string:
		return r.sanitizeString(value)
	case json.Number:
		return r.jsonScalar(value, value.String())
	case bool:
		return r.jsonScalar(value, strconv.FormatBool(value))
	case nil:
		return r.jsonScalar(nil, "null")
	case []any:
		return r.sanitizeArray(value)
	case map[string]any:
		return r.sanitizeObject(value)
	default:
		return r.Mask()
	}
}

func (r *Redactor) sanitizeString(value string) string {
	if r.matchesString(value) {
		return r.Mask()
	}
	return r.Redact(value)
}

func (r *Redactor) matchesString(value string) bool {
	if r == nil || r.matcher == nil {
		return false
	}
	if r.ContainsValue(value) || r.ContainsValue(strconv.Quote(value)) {
		return true
	}
	encoded, _ := json.Marshal(value)
	return r.ContainsValue(string(encoded))
}

func (r *Redactor) literalCrossesJSONValues(text string) bool {
	lengths := r.matchLengths(text, len(text))
	if len(lengths) == 0 {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	for index := nextMatch(lengths, 0); index < len(lengths); {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err != nil {
			return true
		}
		end := int(decoder.InputOffset())
		if index >= end {
			continue
		}
		if !r.masksToken(token) {
			return true
		}
		start := end - len(strings.TrimLeft(text[before:end], ",: \t\r\n"))
		index = matchesWithinToken(lengths, index, start, end)
		if index < 0 {
			return true
		}
	}
	return false
}

func (r *Redactor) masksToken(token json.Token) bool {
	switch value := token.(type) {
	case json.Delim:
		return false
	case string:
		return r.matchesString(value)
	default:
		return true
	}
}

func matchesWithinToken(lengths []int, index, start, end int) int {
	for index < end && index < len(lengths) {
		if index < start || index+lengths[index] > end {
			return -1
		}
		index = nextMatch(lengths, index+1)
	}
	return index
}

func nextMatch(lengths []int, index int) int {
	for index < len(lengths) && lengths[index] == 0 {
		index++
	}
	return index
}

func (r *Redactor) sanitizeArray(values []any) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = r.SanitizeJSON(value)
	}
	return result
}

func (r *Redactor) jsonScalar(value any, rendered string) any {
	if r.ContainsValue(rendered) {
		return r.Mask()
	}
	return value
}

func (r *Redactor) sanitizeObject(values map[string]any) map[string]any {
	keys := make([]string, 0, len(values))
	display := make(map[string]string, len(values))
	reserved := make(map[string]bool, len(values))
	for key := range values {
		keys = append(keys, key)
		cleaned := r.sanitizeString(key)
		display[key] = cleaned
		reserved[cleaned] = true
	}
	slices.Sort(keys)
	output := make(map[string]any, len(values))
	lastAlias := make(map[string]int)
	for _, key := range keys {
		cleaned := display[key]
		if _, exists := output[cleaned]; exists {
			cleaned = unusedKey(cleaned, reserved, lastAlias)
		}
		if mask, sensitive := r.MaskCredentialField(key); sensitive {
			output[cleaned] = mask
		} else {
			output[cleaned] = r.SanitizeJSON(values[key])
		}
	}
	return output
}

func unusedKey(key string, reserved map[string]bool, lastAlias map[string]int) string {
	for number := lastAlias[key] + 1; ; number++ {
		candidate := key + "_" + strconv.Itoa(number)
		if !reserved[candidate] {
			reserved[candidate] = true
			lastAlias[key] = number
			return candidate
		}
	}
}
