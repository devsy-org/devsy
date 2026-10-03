// Package envstore persists plaintext managed environment values. It has no
// dependency on secret backends or unlock credentials.
package envstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/gofrs/flock"
	"sigs.k8s.io/yaml"
)

const FileName = "env.yaml"

var (
	ErrNotFound = errors.New("environment variable not found")
	namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf(
			"invalid environment variable name %q: must start with a letter or underscore and "+
				"contain only letters, digits, and underscores",
			name,
		)
	}
	return nil
}

type EnvValue struct {
	Name     string    `json:"-"`
	Context  string    `json:"-"`
	Value    string    `json:"value"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
}

type EnvStore interface {
	Set(contextName, name, value string) error
	Get(contextName, name string) (string, error)
	Meta(contextName, name string) (EnvValue, error)
	List(contextName string) ([]EnvValue, error)
	Delete(contextName, name string) error
}

type data struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Contexts      map[string]map[string]EnvValue `json:"contexts"`
}

type localStore struct {
	dir   string
	now   func() time.Time
	write func(string, []byte, os.FileMode) error
}

func NewStoreForConfig(_ *config.Config) (EnvStore, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Dir(path)), nil
}

func NewStore(dir string) EnvStore {
	return &localStore{dir: dir, now: time.Now, write: atomicWriteFile}
}

func (s *localStore) Set(contextName, name, value string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	unlock, err := s.locked()
	if err != nil {
		return err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	if d.Contexts[contextName] == nil {
		d.Contexts[contextName] = map[string]EnvValue{}
	}
	entry, exists := d.Contexts[contextName][name]
	if !exists {
		entry.Created = s.now().UTC()
	}
	entry.Value = value
	d.Contexts[contextName][name] = entry
	return s.save(d)
}

func (s *localStore) Meta(contextName, name string) (EnvValue, error) {
	if err := ValidateName(name); err != nil {
		return EnvValue{}, err
	}
	unlock, err := s.locked()
	if err != nil {
		return EnvValue{}, err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return EnvValue{}, err
	}
	value, ok := d.Contexts[contextName][name]
	if !ok {
		return EnvValue{}, ErrNotFound
	}
	value.Name = name
	value.Context = contextName
	return value, nil
}

func (s *localStore) Get(contextName, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	unlock, err := s.locked()
	if err != nil {
		return "", err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return "", err
	}
	value, ok := d.Contexts[contextName][name]
	if !ok {
		return "", ErrNotFound
	}
	value.LastUsed = s.now().UTC()
	d.Contexts[contextName][name] = value
	if err := s.save(d); err != nil {
		return "", err
	}
	return value.Value, nil
}

func (s *localStore) List(contextName string) ([]EnvValue, error) {
	unlock, err := s.locked()
	if err != nil {
		return nil, err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return nil, err
	}
	values := make([]EnvValue, 0, len(d.Contexts[contextName]))
	for name, value := range d.Contexts[contextName] {
		value.Name = name
		value.Context = contextName
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values, nil
}

func (s *localStore) Delete(contextName, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	unlock, err := s.locked()
	if err != nil {
		return err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	delete(d.Contexts[contextName], name)
	if len(d.Contexts[contextName]) == 0 {
		delete(d.Contexts, contextName)
	}
	return s.save(d)
}

// Lock order is config lock -> secrets.yaml.lock -> env.yaml.lock. The shared
// catalog lock prevents migration overwriting concurrent secret mutations.
func (s *localStore) locked() (func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	releases := make([]func(), 0, 2)
	for _, name := range []string{"secrets.yaml.lock", FileName + ".lock"} {
		l := flock.New(filepath.Join(s.dir, name))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		ok, err := l.TryLockContext(ctx, 50*time.Millisecond)
		cancel()
		if err != nil || !ok {
			for _, release := range slices.Backward(releases) {
				release()
			}
			return nil, fmt.Errorf(
				"lock %s: %w",
				name,
				errors.Join(err, errors.New("environment store lock unavailable")),
			)
		}
		releases = append(releases, func() { _ = l.Unlock() })
	}
	return func() {
		for _, release := range slices.Backward(releases) {
			release()
		}
	}, nil
}

func (s *localStore) load() (*data, error) {
	d := &data{SchemaVersion: 1, Contexts: map[string]map[string]EnvValue{}}
	// #nosec G304 -- path is under the selected config directory.
	raw, err := os.ReadFile(filepath.Join(s.dir, FileName))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(raw, d); err != nil {
			return nil, fmt.Errorf("parse environment store: %w", err)
		}
		if d.SchemaVersion != 1 {
			return nil, fmt.Errorf("unsupported environment schema version %d", d.SchemaVersion)
		}
		if d.Contexts == nil {
			d.Contexts = map[string]map[string]EnvValue{}
		}
	}
	if err := s.migrate(d); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *localStore) save(d *data) error {
	raw, err := yaml.Marshal(d)
	if err != nil {
		return err
	}
	return s.write(filepath.Join(s.dir, FileName), raw, 0o600)
}

// migrate retains arbitrary catalog metadata and moves only explicitly tagged
// legacy env records. The env file is committed first: retries preserve newer
// env values if interruption leaves a legacy duplicate behind.
func (s *localStore) migrate(d *data) error {
	path := filepath.Join(s.dir, "secrets.yaml")
	catalog, err := loadMigrationCatalog(path)
	if err != nil {
		return err
	}
	contexts, _ := catalog["contexts"].(map[string]any)
	removed, err := migrateEnvContexts(d, contexts)
	if err != nil {
		return err
	}
	if removed == 0 {
		return nil
	}
	if err := s.save(d); err != nil {
		return fmt.Errorf("migrate environment values: %w", err)
	}
	catalog["schemaVersion"] = 2
	encoded, err := yaml.Marshal(catalog)
	if err != nil {
		return err
	}
	return s.write(path, encoded, 0o600)
}

func loadMigrationCatalog(path string) (map[string]any, error) {
	// #nosec G304 -- path is secrets.yaml under the selected config directory.
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var catalog map[string]any
	// Independent env operations remain usable if the secret catalog is corrupt
	// or its schema semantics are unknown to this implementation.
	if err := yaml.Unmarshal(raw, &catalog); err != nil {
		return nil, nil
	}
	if !supportedMigrationSchema(catalog) {
		return nil, nil
	}
	return catalog, nil
}

func supportedMigrationSchema(catalog map[string]any) bool {
	rawVersion, exists := catalog["schemaVersion"]
	if !exists {
		return true
	}
	version, valid := rawVersion.(float64)
	return valid && (version == 0 || version == 1 || version == 2)
}

func migrateEnvContexts(d *data, contexts map[string]any) (int, error) {
	total := 0
	for contextName, rawEntries := range contexts {
		entries, ok := rawEntries.(map[string]any)
		if !ok {
			continue
		}
		removed, err := migrateEnvEntries(d, contextName, entries)
		if err != nil {
			return 0, err
		}
		total += removed
		if removed > 0 && len(entries) == 0 {
			delete(contexts, contextName)
		}
	}
	return total, nil
}

func migrateEnvEntries(d *data, contextName string, entries map[string]any) (int, error) {
	removed := 0
	for name, rawEntry := range entries {
		entry, _ := rawEntry.(map[string]any)
		if entry["kind"] != "env" {
			continue
		}
		value, err := decodeLegacyEnvValue(entry)
		if err != nil {
			return 0, fmt.Errorf("parse legacy environment entry %s/%s: %w", contextName, name, err)
		}
		if d.Contexts[contextName] == nil {
			d.Contexts[contextName] = map[string]EnvValue{}
		}
		if _, exists := d.Contexts[contextName][name]; !exists {
			d.Contexts[contextName][name] = value
		}
		delete(entries, name)
		removed++
	}
	return removed, nil
}

func decodeLegacyEnvValue(entry map[string]any) (EnvValue, error) {
	encoded, err := yaml.Marshal(entry)
	if err != nil {
		return EnvValue{}, err
	}
	var value EnvValue
	if err := yaml.Unmarshal(encoded, &value); err != nil {
		return EnvValue{}, err
	}
	return value, nil
}
