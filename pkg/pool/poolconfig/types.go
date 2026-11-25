// Package poolconfig defines the pool.yaml format for agent workspace pools.
package poolconfig

import (
	"encoding/json"
	"errors"
	"time"
)

var errInvalidDuration = errors.New("must be a duration such as 15m")

const (
	APIVersion = "devsy.sh/v1alpha1"
	Kind       = "Pool"
)

// Pool is one pool.yaml document.
type Pool struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       Spec     `json:"spec"`
}

type Metadata struct {
	Name string `json:"name"`
}

type Spec struct {
	Source      Source           `json:"source"`
	Provider    Provider         `json:"provider"`
	Capacity    Capacity         `json:"capacity"`
	Lifecycle   Lifecycle        `json:"lifecycle"`
	Egress      Egress           `json:"egress"`
	Credentials []Credential     `json:"credentials,omitempty"`
	Agents      map[string]Agent `json:"agents"`
	Audit       Audit            `json:"audit"`
}

type Source struct {
	Repo             string `json:"repo"`
	Ref              string `json:"ref,omitempty"`
	DevcontainerPath string `json:"devcontainerPath,omitempty"`
}

type Provider struct {
	Name    string            `json:"name"`
	Options map[string]string `json:"options,omitempty"`
}

type Capacity struct {
	MaxConcurrent int `json:"maxConcurrent"`
	MaxQueued     int `json:"maxQueued"`
	Warm          int `json:"warm"`
}

// Lifecycle durations of zero, omitted or written as "0s", take the default.
type Lifecycle struct {
	SpawnTimeout Duration `json:"spawnTimeout"`
	IdleTimeout  Duration `json:"idleTimeout"`
	TTL          Duration `json:"ttl"`
	JobTimeout   Duration `json:"jobTimeout"`
}

type Agent struct {
	Command     []string          `json:"command"`
	Env         map[string]string `json:"env,omitempty"`
	Credentials []string          `json:"credentials,omitempty"`
}

type Audit struct {
	Dir        string `json:"dir,omitempty"`
	RetainDays int    `json:"retainDays"`
}

// Duration reads and writes Go duration strings such as "15m".
type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON treats null as unset, so the default applies.
func (d *Duration) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errInvalidDuration
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return errInvalidDuration
	}
	d.Duration = v
	return nil
}
