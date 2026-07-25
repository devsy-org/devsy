package secrets

import (
	"fmt"
	"os"
	"sort"

	"sigs.k8s.io/yaml"
)

type indexData struct {
	Contexts map[string]map[string]SecretMeta `json:"contexts"`

	KeySource string `json:"keySource,omitempty"`
}

// index is a YAML metadata cache; it exists because OS keyrings cannot
// enumerate entries. The backend remains the source of truth.
type index struct {
	path string
	data indexData
}

func loadIndex(path string) (*index, error) {
	idx := &index{path: path, data: indexData{Contexts: map[string]map[string]SecretMeta{}}}

	raw, err := os.ReadFile(path) // #nosec G304 -- path derived from config dir.
	if err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}

		return nil, fmt.Errorf("read secrets index: %w", err)
	}

	if err := yaml.Unmarshal(raw, &idx.data); err != nil {
		return nil, fmt.Errorf("parse secrets index: %w", err)
	}
	if idx.data.Contexts == nil {
		idx.data.Contexts = map[string]map[string]SecretMeta{}
	}
	idx.normalizeKinds()

	return idx, nil
}

// normalizeKinds fails safe on an unset Kind by treating the entry as a secret,
// so an older/hand-edited entry is never read as a plaintext env var.
func (i *index) normalizeKinds() {
	for _, entries := range i.data.Contexts {
		for name, meta := range entries {
			if meta.Kind != KindSecret && meta.Kind != KindEnv {
				meta.Kind = KindSecret
				meta.Value = ""
				entries[name] = meta
			}
		}
	}
}

func (i *index) save() error {
	out, err := yaml.Marshal(i.data)
	if err != nil {
		return fmt.Errorf("marshal secrets index: %w", err)
	}

	return atomicWriteFile(i.path, out, 0o600)
}

func (i *index) put(meta SecretMeta) {
	if i.data.Contexts[meta.Context] == nil {
		i.data.Contexts[meta.Context] = map[string]SecretMeta{}
	}
	meta.Orphaned = false
	// A sensitive value must never be persisted inline; it lives in the backend.
	if meta.Sensitive() {
		meta.Value = ""
	}
	i.data.Contexts[meta.Context][meta.Name] = meta
}

func (i *index) get(context, name string) (SecretMeta, bool) {
	meta, ok := i.data.Contexts[context][name]
	return meta, ok
}

func (i *index) remove(context, name string) {
	delete(i.data.Contexts[context], name)
	if len(i.data.Contexts[context]) == 0 {
		delete(i.data.Contexts, context)
	}
}

func (i *index) list(context string) []SecretMeta {
	entries := make([]SecretMeta, 0, len(i.data.Contexts[context]))
	for _, meta := range i.data.Contexts[context] {
		entries = append(entries, meta)
	}
	sort.Slice(entries, func(a, b int) bool {
		return entries[a].Name < entries[b].Name
	})

	return entries
}
