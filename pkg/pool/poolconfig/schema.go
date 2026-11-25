package poolconfig

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	yamlv3 "gopkg.in/yaml.v3"
)

const (
	strTag = "!!str"
	// hiddenKey stands in for a map key in decode errors, since a key may be
	// pasted text.
	hiddenKey = "<key>"
)

var (
	durationType = reflect.TypeFor[Duration]()
	// An explicit !!null tag can carry any text, which a message would echo.
	yamlNullSpellings = []string{"", "~", "null", "Null", "NULL"}
	boolSpellings     = map[string]bool{
		"true": true, "True": true, "TRUE": true,
		"false": false, "False": false, "FALSE": false,
	}
	decimalPattern = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)
)

// fieldPath locates a node. key holds map keys as written, for the line
// table; shown is the path printed in messages.
type fieldPath struct {
	key, shown string
}

func (p fieldPath) field(name string) fieldPath {
	return fieldPath{p.key + "." + name, p.shown + "." + name}
}

// entry quotes the map key in the lookup path, so a key holding dots or
// brackets cannot match another field's path.
func (p fieldPath) entry(name, label string) fieldPath {
	return fieldPath{p.key + "[" + strconv.Quote(name) + "]", p.shown + "." + label}
}

func (p fieldPath) index(i int) fieldPath {
	return fieldPath{fmt.Sprintf("%s[%d]", p.key, i), fmt.Sprintf("%s[%d]", p.shown, i)}
}

// decoder turns the YAML node tree into JSON values for the Go types. Every
// scalar keeps the text it was written with, so no second YAML parser can
// read yes, 0123 or a tagged value differently from this check. It records
// the line of each path for later validation errors.
type decoder struct {
	lines map[string]int
}

func (d *decoder) value(n *yamlv3.Node, t reflect.Type, path fieldPath) (any, error) {
	t = derefType(t)
	if n.Anchor != "" || n.Kind == yamlv3.AliasNode {
		return nil, errNoAnchors(n, path)
	}
	d.record(n, path)
	if isNull(n) {
		return nil, nil
	}
	switch {
	case t == durationType:
		return durationValue(n, path)
	case t.Kind() == reflect.Struct:
		return d.object(n, path, structFields(t), nil)
	case t.Kind() == reflect.Map:
		return d.object(n, path, nil, t.Elem())
	case t.Kind() == reflect.Slice:
		return d.list(n, t.Elem(), path)
	}
	return scalarValue(n, t.Kind(), path)
}

func (d *decoder) record(n *yamlv3.Node, path fieldPath) {
	if path.key != "" {
		d.lines[strings.TrimPrefix(path.key, ".")] = n.Line
	}
}

// object decodes a struct when fields is set, otherwise a map of elem. Field
// names must match exactly: the JSON decoder matches them case-insensitively,
// so "allow" and "Allow" would both decode and the last one would win.
func (d *decoder) object(
	n *yamlv3.Node,
	path fieldPath,
	fields map[string]reflect.Type,
	elem reflect.Type,
) (any, error) {
	if n.Kind != yamlv3.MappingNode || n.Tag != "!!map" {
		return nil, nodeErrorf(n, path, "must be a mapping")
	}
	decode := d.decoderFor(fields)
	out := make(map[string]any, len(n.Content)/2)
	for i := 1; i < len(n.Content); i += 2 {
		key := n.Content[i-1]
		name, err := keyName(key, path)
		if err != nil {
			return nil, err
		}
		if _, dup := out[name]; dup {
			return nil, nodeErrorf(key, path, "duplicate key")
		}
		t, err := fieldType(fields, elem, key, path)
		if err != nil {
			return nil, err
		}
		if out[name], err = decode(
			n.Content[i],
			t,
			childPath(path, name, fields != nil),
		); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func childPath(path fieldPath, name string, isField bool) fieldPath {
	if isField {
		return path.field(name)
	}
	return path.entry(name, hiddenKey)
}

func fieldType(
	fields map[string]reflect.Type,
	elem reflect.Type,
	key *yamlv3.Node,
	path fieldPath,
) (reflect.Type, error) {
	if fields == nil {
		return elem, nil
	}
	if t := fields[key.Value]; t != nil {
		return t, nil
	}
	return nil, nodeErrorf(key, path, "unknown field")
}

func (d *decoder) decoderFor(
	fields map[string]reflect.Type,
) func(*yamlv3.Node, reflect.Type, fieldPath) (any, error) {
	if fields == nil {
		return d.element
	}
	return d.value
}

// element decodes a map value or list item. Null is refused there, since
// the JSON decoder would turn it into an empty string or a zero agent.
func (d *decoder) element(n *yamlv3.Node, t reflect.Type, path fieldPath) (any, error) {
	if isNull(n) {
		return nil, nodeErrorf(n, path, "must not be null")
	}
	return d.value(n, t, path)
}

func (d *decoder) list(n *yamlv3.Node, elem reflect.Type, path fieldPath) (any, error) {
	if n.Kind != yamlv3.SequenceNode || n.Tag != "!!seq" {
		return nil, nodeErrorf(n, path, "must be a list")
	}
	out := make([]any, len(n.Content))
	for i, c := range n.Content {
		v, err := d.element(c, elem, path.index(i))
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// keyName takes a key as written. A merge key or anchor could hide a key from
// the exact-name check.
func keyName(k *yamlv3.Node, path fieldPath) (string, error) {
	if k.Anchor != "" || k.Kind == yamlv3.AliasNode || k.Tag == "!!merge" {
		return "", errNoAnchors(k, path)
	}
	if k.Kind != yamlv3.ScalarNode || (isExplicitlyTagged(k) && k.Tag != strTag) {
		return "", nodeErrorf(k, path, "keys must be strings")
	}
	return k.Value, nil
}

type scalarRule struct {
	desc string
	read func(*yamlv3.Node) (any, bool)
}

var scalarRules = map[reflect.Kind]scalarRule{
	reflect.String: {"a string", func(n *yamlv3.Node) (any, bool) {
		return n.Value, !isExplicitlyTagged(n) || n.Tag == strTag
	}},
	reflect.Int: {"a whole number", func(n *yamlv3.Node) (any, bool) {
		return json.Number(n.Value), isDecimal(n)
	}},
	reflect.Bool: {"true or false", func(n *yamlv3.Node) (any, bool) {
		b, ok := boolSpellings[n.Value]
		return b, ok && n.Tag == "!!bool"
	}},
}

func scalarValue(n *yamlv3.Node, kind reflect.Kind, path fieldPath) (any, error) {
	rule, known := scalarRules[kind]
	if !known {
		return nil, nodeErrorf(n, path, "has an unsupported type")
	}
	v, ok := rule.read(n)
	if !ok || n.Kind != yamlv3.ScalarNode {
		return nil, nodeErrorf(n, path, "must be %s", rule.desc)
	}
	return v, nil
}

// isDecimal also refuses values that overflow int64, which the JSON decoder
// would report without the line.
func isDecimal(n *yamlv3.Node) bool {
	_, err := strconv.ParseInt(n.Value, 10, 64)
	return n.Tag == "!!int" && decimalPattern.MatchString(n.Value) && err == nil
}

func durationValue(n *yamlv3.Node, path fieldPath) (any, error) {
	if _, err := time.ParseDuration(n.Value); err != nil || n.Kind != yamlv3.ScalarNode ||
		n.Tag != strTag {
		return nil, nodeErrorf(n, path, "must be a duration string such as \"15m\"")
	}
	return n.Value, nil
}

// isNull accepts only plain scalar nulls: an explicit !!null tag can sit on
// a collection, carry any text or turn a quoted string into null.
func isNull(n *yamlv3.Node) bool {
	return n.Kind == yamlv3.ScalarNode && n.Tag == "!!null" && !isExplicitlyTagged(n) &&
		slices.Contains(yamlNullSpellings, n.Value)
}

func isExplicitlyTagged(n *yamlv3.Node) bool {
	return n.Style&yamlv3.TaggedStyle != 0
}

func structFields(t reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for field := range t.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		fields[name] = field.Type
	}
	return fields
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func errNoAnchors(n *yamlv3.Node, path fieldPath) error {
	return nodeErrorf(n, path, "YAML anchors and aliases are not supported")
}

func nodeErrorf(n *yamlv3.Node, path fieldPath, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s (line %d): %s", displayPath(path.shown), n.Line, msg)
}

func displayPath(path string) string {
	if path == "" {
		return "document"
	}
	return strings.TrimPrefix(path, ".")
}
