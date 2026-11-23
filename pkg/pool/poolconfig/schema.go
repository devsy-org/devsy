package poolconfig

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	yamlv2 "go.yaml.in/yaml/v2"
	yamlv3 "gopkg.in/yaml.v3"
)

const strTag = "!!str"

var (
	durationType = reflect.TypeFor[Duration]()
	// An explicit !!null tag can carry any text, which the decoder would echo.
	yamlNullSpellings = []string{"", "~", "null", "Null", "NULL"}
	// YAML 1.1 reads 010 as octal and 1_000 as 1000; only plain decimals agree.
	decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

// shape is the YAML node a Go field type needs.
type shape struct {
	kind yamlv3.Kind
	tag  string
	desc string
}

// checkNodes rejects anchors, aliases and map keys the decoder would resolve
// differently from how they read: an alias can hide a key from the exact-name
// check, and a plain key such as 1 or ON is not a string under YAML 1.1.
func checkNodes(n *yamlv3.Node, path string) error {
	if n.Anchor != "" || n.Kind == yamlv3.AliasNode {
		return errNoAnchors(n, path)
	}
	if n.Kind == yamlv3.MappingNode {
		return checkMapping(n, path)
	}
	for _, c := range n.Content {
		if err := checkNodes(c, path); err != nil {
			return err
		}
	}
	return nil
}

func checkMapping(n *yamlv3.Node, path string) error {
	seen := make(map[string]bool, len(n.Content)/2)
	for i := 1; i < len(n.Content); i += 2 {
		key := n.Content[i-1]
		if err := checkKey(key, path); err != nil {
			return err
		}
		if seen[key.Value] {
			return nodeErrorf(key, path, "duplicate key %q", key.Value)
		}
		seen[key.Value] = true
		if err := checkNodes(n.Content[i], path+"."+key.Value); err != nil {
			return err
		}
	}
	return nil
}

func errNoAnchors(n *yamlv3.Node, path string) error {
	return nodeErrorf(n, path, "YAML anchors and aliases are not supported")
}

func nodeErrorf(n *yamlv3.Node, path, format string, args ...any) error {
	return fmt.Errorf("%s (line %d): %s", displayPath(path), n.Line, fmt.Sprintf(format, args...))
}

func checkKey(k *yamlv3.Node, path string) error {
	if k.Anchor != "" || k.Kind == yamlv3.AliasNode || k.Tag == "!!merge" {
		return errNoAnchors(k, path)
	}
	if !isStringKey(k) {
		return nodeErrorf(k, path, "key %q must be a quoted string", k.Value)
	}
	return nil
}

func isStringKey(k *yamlv3.Node) bool {
	switch {
	case k.Kind != yamlv3.ScalarNode:
		return false
	case k.Style&yamlv3.TaggedStyle != 0:
		return k.Tag == strTag
	case k.Style&(yamlv3.SingleQuotedStyle|yamlv3.DoubleQuotedStyle|
		yamlv3.LiteralStyle|yamlv3.FoldedStyle) != 0:
		return true
	}
	return resolvesAsString(k.Value)
}

// resolvesAsString decodes the key in a mapping with the YAML 1.1 decoder
// sigs.k8s.io/yaml uses, where plain keys such as ON, 1_000 or 0x1f are not
// strings but 2026-01-15 is.
func resolvesAsString(plain string) bool {
	var out map[any]any
	// Explicit key syntax has no 1024 character limit on the key.
	if err := yamlv2.Unmarshal([]byte("? "+plain+"\n: 0\n"), &out); err != nil || len(out) != 1 {
		return false
	}
	_, ok := out[plain]
	return ok
}

func displayPath(path string) string {
	if path == "" {
		return "document"
	}
	return strings.TrimPrefix(path, ".")
}

func checkDuration(n *yamlv3.Node, path string) error {
	if _, err := time.ParseDuration(n.Value); err != nil || n.Tag != strTag {
		return nodeErrorf(n, path, "must be a duration string such as \"15m\"")
	}
	return nil
}

// checkFieldNames requires every key to match a json tag exactly. The JSON
// decoder matches names case-insensitively, so "allow" and "Allow" in one
// mapping would both decode and the last one would silently win.
func checkFieldNames(n *yamlv3.Node, t reflect.Type, path string) error {
	t = derefType(t)
	if err := checkShape(n, t, path); err != nil {
		return err
	}
	switch {
	case t.Kind() == reflect.Struct && n.Kind == yamlv3.MappingNode:
		return checkStructKeys(n, t, path)
	case t.Kind() == reflect.Map && n.Kind == yamlv3.MappingNode:
		return checkMapValues(n, t.Elem(), path)
	case t.Kind() == reflect.Slice && n.Kind == yamlv3.SequenceNode:
		return checkItems(n.Content, t.Elem(), path)
	}
	return nil
}

// checkShape reports a value of the wrong kind with its full path and line.
// The JSON decoder would name only the innermost struct field, without map
// keys, list indexes or the line.
func checkShape(n *yamlv3.Node, t reflect.Type, path string) error {
	if n.Kind == yamlv3.AliasNode || isNull(n) {
		return nil
	}
	if t == durationType {
		return checkDuration(n, path)
	}
	want, ok := shapeOf(t)
	if !ok {
		return nil
	}
	if !want.matches(n) {
		return nodeErrorf(n, path, "must be %s", want.desc)
	}
	if t.Kind() == reflect.String && changesText(n) {
		return nodeErrorf(n, path, "must be quoted to keep its text")
	}
	return nil
}

// isNull accepts only scalar nulls: an explicit !!null tag can sit on a
// collection or carry any text, which later checks would echo.
func isNull(n *yamlv3.Node) bool {
	return n.Kind == yamlv3.ScalarNode && n.Tag == "!!null" &&
		slices.Contains(yamlNullSpellings, n.Value)
}

// changesText finds an unquoted value that the YAML 1.1 decoder reads as a
// number or bool with different text, so yes or 0123 in a string field would
// silently become "true" or "83". The text is reparsed on its own, where a
// value such as - can read as a list; only scalar results count.
func changesText(n *yamlv3.Node) bool {
	switch {
	case n.Style&(yamlv3.SingleQuotedStyle|yamlv3.DoubleQuotedStyle|
		yamlv3.LiteralStyle|yamlv3.FoldedStyle) != 0:
		return false
	case n.Style&yamlv3.TaggedStyle != 0:
		return n.Tag != strTag
	}
	var v any
	if err := yamlv2.Unmarshal([]byte(n.Value), &v); err != nil {
		return false
	}
	switch v.(type) {
	case int, int64, bool:
		return fmt.Sprint(v) != n.Value
	case uint64, float64:
		return true
	}
	return false
}

func shapeOf(t reflect.Type) (shape, bool) {
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		return shape{kind: yamlv3.MappingNode, desc: "a mapping"}, true
	case reflect.Slice:
		return shape{kind: yamlv3.SequenceNode, desc: "a list"}, true
	case reflect.String:
		return shape{kind: yamlv3.ScalarNode, desc: "a string"}, true
	case reflect.Int, reflect.Int32, reflect.Int64:
		return shape{kind: yamlv3.ScalarNode, tag: "!!int", desc: "a whole number"}, true
	case reflect.Bool:
		return shape{kind: yamlv3.ScalarNode, tag: "!!bool", desc: "true or false"}, true
	default:
		return shape{}, false
	}
}

func (s shape) matches(n *yamlv3.Node) bool {
	if n.Kind != s.kind || (s.tag != "" && n.Tag != s.tag) {
		return false
	}
	return s.tag != "!!int" || decimalPattern.MatchString(n.Value)
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func checkMapValues(n *yamlv3.Node, elem reflect.Type, path string) error {
	for i := 1; i < len(n.Content); i += 2 {
		key := n.Content[i-1]
		// checkNodes rejects merge keys with a clearer message.
		if key.Tag == "!!merge" {
			continue
		}
		if err := checkFieldNames(n.Content[i], elem, path+"."+key.Value); err != nil {
			return err
		}
	}
	return nil
}

func checkItems(nodes []*yamlv3.Node, t reflect.Type, path string) error {
	for i, c := range nodes {
		if err := checkFieldNames(c, t, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}

func checkStructKeys(n *yamlv3.Node, t reflect.Type, path string) error {
	fields := map[string]reflect.Type{}
	for field := range t.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		fields[name] = field.Type
	}
	for i := 1; i < len(n.Content); i += 2 {
		key := n.Content[i-1].Value
		ft, ok := fields[key]
		if !ok {
			return nodeErrorf(n.Content[i-1], path, "unknown field %q", key)
		}
		if err := checkFieldNames(n.Content[i], ft, path+"."+key); err != nil {
			return err
		}
	}
	return nil
}
