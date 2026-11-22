package poolconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	yamlv3 "gopkg.in/yaml.v3"
	"sigs.k8s.io/yaml"
)

const (
	defaultSpawnTimeout = 10 * time.Minute
	defaultIdleTimeout  = 15 * time.Minute
	defaultTTL          = 8 * time.Hour
	defaultJobTimeout   = 2 * time.Hour
	defaultRetainDays   = 30
)

// Load reads, defaults and validates a pool.yaml file.
func Load(path string) (*Pool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the pool file the operator named
	if err != nil {
		return nil, fmt.Errorf("read pool config: %w", err)
	}
	return Parse(data)
}

// Parse decodes pool.yaml content, rejecting unknown fields, then applies
// defaults and validates the result.
func Parse(data []byte) (*Pool, error) {
	if err := checkDocument(data); err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	var p Pool
	if err := yaml.UnmarshalStrict(data, &p); err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	p.ApplyDefaults()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// PolicyVersion hashes the defaulted pool, so files that differ only in
// formatting, key order or spelled-out defaults share a version.
func (p *Pool) PolicyVersion() (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encode pool config: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (p *Pool) ApplyDefaults() {
	s := &p.Spec
	if s.Capacity.MaxConcurrent == 0 {
		s.Capacity.MaxConcurrent = 1
	}
	defaultDuration(&s.Lifecycle.SpawnTimeout, defaultSpawnTimeout)
	defaultDuration(&s.Lifecycle.IdleTimeout, defaultIdleTimeout)
	defaultDuration(&s.Lifecycle.TTL, defaultTTL)
	defaultDuration(&s.Lifecycle.JobTimeout, defaultJobTimeout)
	s.applyEgressDefaults()
	for i := range s.Credentials {
		defaultCredential(&s.Credentials[i])
	}
	if s.Audit.RetainDays == 0 {
		s.Audit.RetainDays = defaultRetainDays
	}
}

// checkDocument rejects input that UnmarshalStrict would accept but not fully
// apply: a second YAML document, or field names that differ only in case.
func checkDocument(data []byte) error {
	dec := yamlv3.NewDecoder(bytes.NewReader(data))
	var node yamlv3.Node
	if err := dec.Decode(&node); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := checkFieldNames(&node, reflect.TypeFor[Pool](), ""); err != nil {
		return err
	}
	if err := dec.Decode(&node); !errors.Is(err, io.EOF) {
		return errors.New("file must contain exactly one YAML document")
	}
	return nil
}

func defaultDuration(d *Duration, value time.Duration) {
	if d.Duration == 0 {
		d.Duration = value
	}
}
