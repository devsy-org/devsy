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
	"regexp"
	"time"

	yamlv3 "gopkg.in/yaml.v3"
)

const (
	defaultSpawnTimeout = 10 * time.Minute
	defaultIdleTimeout  = 15 * time.Minute
	defaultTTL          = 8 * time.Hour
	defaultJobTimeout   = 2 * time.Hour
	defaultRetainDays   = 30
)

var yamlLinePattern = regexp.MustCompile(`^(?:error converting YAML to JSON: )?yaml: line (\d+):`)

// Load reads, defaults and validates a pool.yaml file.
func Load(path string) (*Pool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the pool file the operator named
	if err != nil {
		return nil, fmt.Errorf("read pool config: %w", err)
	}
	return Parse(data)
}

// Parse decodes pool.yaml content, rejecting unknown fields, then applies
// defaults and validates the result. Errors give the line where one exists.
func Parse(data []byte) (*Pool, error) {
	tree, lines, err := decodeDocument(data)
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	raw, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	var p Pool
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse pool config: %w", decodeError(err))
	}
	p.ApplyDefaults()
	if err := p.validate(lines); err != nil {
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
	if s.Audit.RetainDays == 0 {
		s.Audit.RetainDays = defaultRetainDays
	}
}

// decodeDocument reads exactly one YAML document into JSON values and the
// line of each field path.
func decodeDocument(data []byte) (any, map[string]int, error) {
	dec := yamlv3.NewDecoder(bytes.NewReader(data))
	var node yamlv3.Node
	if err := dec.Decode(&node); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, nil
		}
		return nil, nil, decodeError(err)
	}
	d := decoder{lines: map[string]int{}}
	var tree any
	for _, root := range node.Content {
		var err error
		if tree, err = d.value(root, reflect.TypeFor[Pool](), ""); err != nil {
			return nil, nil, err
		}
	}
	var extra yamlv3.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("file must contain exactly one YAML document")
	}
	return tree, d.lines, nil
}

func defaultDuration(d *Duration, value time.Duration) {
	if d.Duration == 0 {
		d.Duration = value
	}
}

// decodeError keeps only the field path, expected type and line of a decoder
// error. Decoders quote the offending value, which may be a pasted secret, so
// nothing is taken from the free text after the decoder's fixed prefix.
func decodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" && typeErr.Type != nil {
		return fmt.Errorf("%s: must be of type %s", typeErr.Field, typeErr.Type)
	}
	if m := yamlLinePattern.FindStringSubmatch(err.Error()); m != nil {
		return fmt.Errorf("invalid YAML at line %s", m[1])
	}
	return errors.New("invalid YAML or a value of the wrong type")
}
