package poolconfig

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	yamlv2 "go.yaml.in/yaml/v2"
	yamlv3 "gopkg.in/yaml.v3"
)

var (
	durationType = reflect.TypeFor[Duration]()
	// An explicit !!null tag can carry any text, which the decoder would echo.
	yamlNullSpellings = []string{"", "~", "null", "Null", "NULL"}
)

// checkNodes rejects anchors, aliases and map keys the decoder would resolve
// differently from how they read: an alias can hide a key from the exact-name
// check, and a plain key such as 1 or ON is not a string under YAML 1.1.
func checkNodes(n *yamlv3.Node, path string) error {
	if n.Anchor != "" || n.Kind == yamlv3.AliasNode {
		return errNoAnchors(path)
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
	for i := 1; i < len(n.Content); i += 2 {
		key := n.Content[i-1]
		if err := checkKey(key, path); err != nil {
			return err
		}
		if err := checkNodes(n.Content[i], path+"."+key.Value); err != nil {
			return err
		}
	}
	return nil
}

func errNoAnchors(path string) error {
	return fmt.Errorf("%s: YAML anchors and aliases are not supported", displayPath(path))
}

func checkKey(k *yamlv3.Node, path string) error {
	if k.Anchor != "" || k.Kind == yamlv3.AliasNode || k.Tag == "!!merge" {
		return errNoAnchors(path)
	}
	if !isStringKey(k) {
		return fmt.Errorf("%s: key %q must be a quoted string", displayPath(path), k.Value)
	}
	return nil
}

func isStringKey(k *yamlv3.Node) bool {
	if k.Kind != yamlv3.ScalarNode || k.Tag != "!!str" {
		return false
	}
	if k.Style&(yamlv3.SingleQuotedStyle|yamlv3.DoubleQuotedStyle) != 0 {
		return true
	}
	return resolvesAsString(k.Value)
}

// resolvesAsString uses the YAML 1.1 resolver the decoder uses, where plain
// keys such as ON, 1_000 or 0x1f are not strings.
func resolvesAsString(plain string) bool {
	var out any
	if err := yamlv2.Unmarshal([]byte(plain), &out); err != nil {
		return false
	}
	str, ok := out.(string)
	return ok && str == plain
}

func displayPath(path string) string {
	if path == "" {
		return "document"
	}
	return strings.TrimPrefix(path, ".")
}

func checkDuration(n *yamlv3.Node, path string) error {
	if n.Tag == "!!null" && slices.Contains(yamlNullSpellings, n.Value) {
		return nil
	}
	if _, err := time.ParseDuration(n.Value); err != nil || n.Tag != "!!str" {
		return fmt.Errorf("%s: must be a duration string such as \"15m\"", displayPath(path))
	}
	return nil
}

// checkFieldNames requires every key to match a json tag exactly. The JSON
// decoder matches names case-insensitively, so "allow" and "Allow" in one
// mapping would both decode and the last one would silently win.
func checkFieldNames(n *yamlv3.Node, t reflect.Type, path string) error {
	t = derefType(t)
	switch {
	case n.Kind == yamlv3.DocumentNode:
		return checkChildren(n.Content, t, path)
	case t.Kind() == reflect.Struct && n.Kind == yamlv3.MappingNode:
		return checkStructKeys(n, t, path)
	case t.Kind() == reflect.Map && n.Kind == yamlv3.MappingNode:
		return checkMapValues(n, t.Elem(), path)
	case t.Kind() == reflect.Slice && n.Kind == yamlv3.SequenceNode:
		return checkChildren(n.Content, t.Elem(), path)
	}
	return nil
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func checkMapValues(n *yamlv3.Node, elem reflect.Type, path string) error {
	for i := 1; i < len(n.Content); i += 2 {
		if err := checkFieldNames(n.Content[i], elem, path+"."+n.Content[i-1].Value); err != nil {
			return err
		}
	}
	return nil
}

func checkChildren(nodes []*yamlv3.Node, t reflect.Type, path string) error {
	for _, c := range nodes {
		if err := checkFieldNames(c, t, path); err != nil {
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
			return fmt.Errorf("%s: unknown field %q", strings.TrimPrefix(path, "."), key)
		}
		var err error
		if ft == durationType && n.Content[i].Kind == yamlv3.ScalarNode {
			err = checkDuration(n.Content[i], path+"."+key)
		} else {
			err = checkFieldNames(n.Content[i], ft, path+"."+key)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
