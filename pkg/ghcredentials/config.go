package ghcredentials

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"

	"gopkg.in/yaml.v3"
)

const (
	hostsFile = "hosts.yml"
	// #nosec G101 -- YAML field name, not a credential.
	oauthTokenKey = "oauth_token"
	activeUserKey = "user"
	configFile    = "config.yml"
)

func ConfigDir() (string, error) {
	if dir := os.Getenv("GH_CONFIG_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Abs(filepath.Join(dir, "gh"))
	}
	if dir := os.Getenv("APPDATA"); runtime.GOOS == "windows" && dir != "" {
		return filepath.Abs(filepath.Join(dir, "GitHub CLI"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "gh"), nil
}

func resolvedFile(path string) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	return os.OpenInRoot(filepath.Dir(resolved), filepath.Base(resolved))
}

func readFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, errInvalidConfig
	}
	f, err := resolvedFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if len(data) > 16<<20 {
		return nil, errors.New("GitHub CLI configuration file too large")
	}
	return data, err
}

func decode(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		// YAML errors may include credential values.
		return nil, errInvalidConfig
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errInvalidConfig
	}
	if len(doc.Content) == 0 {
		return mapping(), nil
	}
	n := doc.Content[0]
	if n.Kind != yaml.MappingNode {
		return nil, errInvalidConfig
	}
	return n, nil
}

func get(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func put(n *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			if value == nil {
				n.Content = append(n.Content[:i], n.Content[i+2:]...)
			} else {
				n.Content[i+1] = value
			}
			return
		}
	}
	if value != nil {
		n.Content = append(n.Content, scalar(key), value)
	}
}

func scalar(
	s string,
) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func equal(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	var av, bv any
	if a.Decode(&av) != nil || b.Decode(&bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func merge(before, after, latest *yaml.Node) *yaml.Node {
	if equal(before, after) {
		return latest
	}
	if mappingCompatible(before, after) {
		if before == nil {
			before = mapping()
		}
		if latest == nil || latest.Kind != yaml.MappingNode {
			latest = mapping()
		}
		for key := range mappingKeys(before, after) {
			put(latest, key, merge(get(before, key), get(after, key), get(latest, key)))
		}
		return latest
	}
	return after
}

func mergeYAML(before, after, latest []byte) ([]byte, error) {
	b, err := decode(before)
	if err != nil {
		return nil, err
	}
	a, err := decode(after)
	if err != nil {
		return nil, err
	}
	l, err := decode(latest)
	if err != nil {
		return nil, err
	}
	result := merge(b, a, l)
	if equal(result, l) && equal(b, a) {
		return latest, nil
	}
	return yaml.Marshal(result)
}

func mappingKeys(nodes ...*yaml.Node) map[string]bool {
	keys := map[string]bool{}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			keys[n.Content[i].Value] = true
		}
	}
	return keys
}

func mappingCompatible(before, after *yaml.Node) bool {
	if after == nil || after.Kind != yaml.MappingNode {
		return false
	}
	return before == nil || before.Kind == yaml.MappingNode
}
