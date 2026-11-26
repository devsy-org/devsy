package redact

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (r *Redactor) redactJSON(text string) (string, bool) {
	value, encoded, valid := r.sanitizeDocument(text)
	if valid && text != encoded && utf8.ValidString(text) && !r.ContainsValue(text) {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if sameJSONValue(decoder, value) {
			return text, true
		}
	}
	return encoded, valid
}

func sameJSONValue(decoder *json.Decoder, value any) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch token {
	case json.Delim('{'):
		object, ok := value.(map[string]any)
		return ok && sameJSONObject(decoder, object)
	case json.Delim('['):
		array, ok := value.([]any)
		return ok && sameJSONArray(decoder, array)
	default:
		return token == value
	}
}

func sameJSONObject(decoder *json.Decoder, object map[string]any) bool {
	seen := make(map[string]bool, len(object))
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		child, exists := object[key]
		if !exists || !sameJSONValue(decoder, child) {
			return false
		}
		seen[key] = true
	}
	return len(seen) == len(object) && closeJSONValue(decoder, '}')
}

func sameJSONArray(decoder *json.Decoder, array []any) bool {
	for _, child := range array {
		if !decoder.More() || !sameJSONValue(decoder, child) {
			return false
		}
	}
	return closeJSONValue(decoder, ']')
}

func closeJSONValue(decoder *json.Decoder, end json.Delim) bool {
	token, err := decoder.Token()
	return err == nil && token == end
}

func (r *Redactor) SanitizeJSON(data []byte) any {
	value, _, valid := r.sanitizeDocument(string(data))
	if !valid {
		return r.Mask()
	}
	return value
}

func (r *Redactor) sanitizeDocument(text string) (any, string, bool) {
	if !json.Valid([]byte(text)) {
		return nil, "", false
	}
	if r.literalCrossesJSONValues(text) {
		return r.Mask(), r.jsonMask(), true
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return r.Mask(), r.jsonMask(), true
	}
	if r.unsafeUnescapedJSON(value, text, r.literalCrossesJSONValues) {
		return r.Mask(), r.jsonMask(), true
	}
	value = r.sanitizeJSONValue(value)
	result, err := json.Marshal(value)
	if err != nil || r.ContainsValue(string(result)) ||
		r.unsafeUnescapedJSON(value, string(result), r.ContainsValue) {
		return r.Mask(), r.jsonMask(), true
	}
	return value, string(result), true
}

func (r *Redactor) unsafeUnescapedJSON(value any, encoded string, matches func(string) bool) bool {
	if r == nil || r.matcher == nil || !strings.Contains(encoded, `\u00`) {
		return false
	}
	var rendered bytes.Buffer
	encoder := json.NewEncoder(&rendered)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value) != nil || matches(strings.TrimSuffix(rendered.String(), "\n"))
}

func (r *Redactor) jsonMask() string {
	encoded, _ := json.Marshal(r.Mask())
	return string(encoded)
}

func (r *Redactor) sanitizeJSONValue(value any) any {
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
		result[i] = r.sanitizeJSONValue(value)
	}
	return result
}

func (r *Redactor) jsonScalar(value any, rendered string) any {
	if r.ContainsValue(rendered) {
		return r.Mask()
	}
	return value
}

func (r *Redactor) sanitizeObject(values map[string]any) any {
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
			if r.matchesString(cleaned) {
				return r.Mask()
			}
		}
		if mask, sensitive := r.MaskCredentialField(key); sensitive {
			output[cleaned] = mask
		} else {
			output[cleaned] = r.sanitizeJSONValue(values[key])
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
