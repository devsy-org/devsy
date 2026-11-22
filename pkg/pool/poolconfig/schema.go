package poolconfig

import (
	"fmt"
	"reflect"
	"strings"

	yamlv3 "gopkg.in/yaml.v3"
)

// checkFieldNames requires every key to match a json tag exactly. The JSON
// decoder matches names case-insensitively, so "allow" and "Allow" in one
// mapping would both decode and the last one would silently win.
func checkFieldNames(n *yamlv3.Node, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
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
		if err := checkFieldNames(n.Content[i], ft, path+"."+key); err != nil {
			return err
		}
	}
	return nil
}
