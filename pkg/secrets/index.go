package secrets

import (
	"fmt"
	"os"
	"sort"

	"sigs.k8s.io/yaml"
)

type fileStoreMetadata struct {
	KeySource string `json:"keySource,omitempty"`
}

type indexData struct {
	SchemaVersion int                              `json:"schemaVersion,omitempty"`
	FileStore     fileStoreMetadata                `json:"fileStore,omitzero"`
	Contexts      map[string]map[string]SecretMeta `json:"contexts"`

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
		return nil, &StoreCorruptError{Cause: err}
	}
	if err := idx.normalizeSchema(); err != nil {
		return nil, err
	}
	if idx.data.Contexts == nil {
		idx.data.Contexts = map[string]map[string]SecretMeta{}
	}
	idx.normalizeKinds()
	if err := idx.validateBackends(); err != nil {
		return nil, err
	}

	return idx, nil
}

// normalizeKinds fails safe on an unset Kind by treating the entry as a secret,
// so an older/hand-edited entry is never read as a plaintext env var.
func (i *index) normalizeKinds() {
	for contextName, entries := range i.data.Contexts {
		for name, meta := range entries {
			meta.Name = name
			meta.Context = contextName
			if meta.Kind == KindSecret {
				meta.Value = ""
			}
			entries[name] = meta
			if meta.Kind != KindSecret && meta.Kind != KindEnv {
				meta.Kind = KindSecret
				meta.Value = ""
				entries[name] = meta
			}
		}
	}
}

func (i *index) validateBackends() error {
	for context, entries := range i.data.Contexts {
		for name, meta := range entries {
			if err := validateBackend(context, name, meta); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateBackend(context, name string, meta SecretMeta) error {
	if meta.Kind == KindEnv {
		if meta.Backend != "" {
			return fmt.Errorf(
				"invalid backend %q for environment entry %s/%s",
				meta.Backend,
				context,
				name,
			)
		}
		return nil
	}
	if meta.Backend != "" && meta.Backend != BackendKeyring && meta.Backend != BackendFile {
		return fmt.Errorf(
			"invalid secrets backend %q for %s/%s",
			meta.Backend,
			context,
			name,
		)
	}
	return nil
}

func (i *index) save() error {
	data := i.data
	data.SchemaVersion = 2
	// Keep the legacy field synchronized for older installed CLI readers.
	data.FileStore.KeySource = data.KeySource
	out, err := yaml.Marshal(data)
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

func (idx *index) normalizeSchema() error {
	if idx.data.SchemaVersion > 2 {
		return &StoreCorruptError{Cause: fmt.Errorf("unsupported secret catalog schema version")}
	}
	if idx.data.FileStore.KeySource != "" {
		if idx.data.KeySource != "" && idx.data.KeySource != idx.data.FileStore.KeySource {
			return &StoreCorruptError{}
		}
		idx.data.KeySource = idx.data.FileStore.KeySource
	}
	return nil
}
