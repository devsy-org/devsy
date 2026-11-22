package poolconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	defaultHTTPSPort    = 443
	defaultSpawnTimeout = 10 * time.Minute
	defaultIdleTimeout  = 15 * time.Minute
	defaultTTL          = 8 * time.Hour
	defaultJobTimeout   = 2 * time.Hour
	defaultMintTTL      = time.Hour
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
	if s.Egress.Default == "" {
		s.Egress.Default = EgressDeny
	}
	for i := range s.Egress.Allow {
		if len(s.Egress.Allow[i].Ports) == 0 {
			s.Egress.Allow[i].Ports = []int{defaultHTTPSPort}
		}
	}
	for i := range s.Credentials {
		defaultCredential(&s.Credentials[i])
	}
	if s.Audit.RetainDays == 0 {
		s.Audit.RetainDays = defaultRetainDays
	}
}

func defaultCredential(c *Credential) {
	if c.Mode != ModeMint {
		return
	}
	defaultDuration(&c.TTL, defaultMintTTL)
	if len(c.Deliver) == 0 {
		c.Deliver = []string{DeliverGitHelper}
	}
}

func defaultDuration(d *Duration, value time.Duration) {
	if d.Duration == 0 {
		d.Duration = value
	}
}
