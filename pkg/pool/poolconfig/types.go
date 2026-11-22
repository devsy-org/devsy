// Package poolconfig defines the pool.yaml format for agent workspace pools.
package poolconfig

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	APIVersion = "devsy.sh/v1alpha1"
	Kind       = "Pool"

	EgressDeny = "deny"

	ModeMint   = "mint"
	ModeInject = "inject"

	IssuerGitHubApp = "github-app"

	DeliverGitHelper = "git-helper"
	DeliverGHEnv     = "gh-env"
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

type Egress struct {
	Default string      `json:"default"`
	Allow   []AllowRule `json:"allow,omitempty"`
}

// AllowRule permits egress to a host name or an address range. Exactly one
// of Host and CIDR is set.
type AllowRule struct {
	Host  string `json:"host,omitempty"`
	CIDR  string `json:"cidr,omitempty"`
	Ports []int  `json:"ports,omitempty"`
}

// Credential is a grant the broker serves to pool sessions. Mint grants use
// the issuer fields; inject grants use Secret and Route. A zero mint TTL takes
// the default.
type Credential struct {
	Name           string     `json:"name"`
	Mode           string     `json:"mode"`
	Issuer         string     `json:"issuer,omitempty"`
	AppID          string     `json:"appId,omitempty"`
	InstallationID string     `json:"installationId,omitempty"`
	PrivateKey     string     `json:"privateKey,omitempty"`
	Scope          *MintScope `json:"scope,omitempty"`
	TTL            Duration   `json:"ttl"`
	Deliver        []string   `json:"deliver,omitempty"`
	Secret         string     `json:"secret,omitempty"`
	Route          *Route     `json:"route,omitempty"`
}

type MintScope struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

// Route is an inject endpoint on the egress sidecar that forwards to a fixed
// upstream and adds the credential as a header.
type Route struct {
	Name     string `json:"name"`
	Upstream string `json:"upstream"`
	Header   string `json:"header"`
	Prefix   string `json:"prefix,omitempty"`
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

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("duration must be a string such as \"15m\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}
