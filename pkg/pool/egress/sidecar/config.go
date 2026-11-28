// Package sidecar runs the egress proxy as the process inside a session's
// proxy container. It reads the config the pool wrote, serves the proxy and
// reports every decision to the pool over the session's event socket.
package sidecar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	// ConfigVersion is the only egress.json version this binary reads.
	ConfigVersion  = 1
	maxConfigBytes = 64 << 10
	maxSeconds     = 24 * 60 * 60
)

// Config is the content of egress.json. The pool writes it and mounts it
// read-only. It holds no secret. AlwaysDeny is required: an empty list would
// leave the pool networks reachable through a cidr rule.
type Config struct {
	Version   int    `json:"version"`
	SessionID string `json:"sessionId"`
	// Bind is the one interface address and port the proxy listens on.
	Bind string `json:"bind"`
	// EventsSocket is the absolute path of the pool's event socket.
	EventsSocket string            `json:"eventsSocket"`
	Egress       poolconfig.Egress `json:"egress"`
	// AlwaysDeny lists CIDR networks the proxy never dials, such as the pool
	// networks and the host gateway, whatever the allow rules say.
	AlwaysDeny []string `json:"alwaysDeny,omitempty"`
	// MaxConns, IdleSeconds and LifetimeSeconds override the proxy limits.
	// Zero keeps the proxy default.
	MaxConns        int `json:"maxConns,omitempty"`
	IdleSeconds     int `json:"idleSeconds,omitempty"`
	LifetimeSeconds int `json:"lifetimeSeconds,omitempty"`
}

// Load reads and checks an egress.json file.
func Load(path string) (Config, error) {
	// Stat first: opening a FIFO for reading would block before any check.
	if info, err := os.Stat(filepath.Clean(path)); err != nil {
		return Config{}, fmt.Errorf("read egress config: %w", err)
	} else if !info.Mode().IsRegular() {
		return Config{}, errors.New("read egress config: not a regular file")
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Config{}, fmt.Errorf("read egress config: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read egress config: %w", err)
	}
	return Parse(data)
}

// Parse decodes egress.json content, refusing unknown fields and trailing
// data, and checks every part with the code Run uses to build the proxy, so a
// config that parses is one Run can start.
func Parse(data []byte) (Config, error) {
	if len(data) > maxConfigBytes {
		return Config{}, fmt.Errorf("egress config is larger than %d bytes", maxConfigBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse egress config: %w", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return Config{}, errors.New("parse egress config: data after the first object")
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch {
	case c.Version != ConfigVersion:
		return fmt.Errorf("egress config version must be %d", ConfigVersion)
	case !filepath.IsAbs(c.EventsSocket):
		return errors.New("egress config eventsSocket must be an absolute path")
	case len(c.AlwaysDeny) == 0:
		return errors.New("egress config alwaysDeny must list the pool networks")
	}
	if err := c.validateLimits(); err != nil {
		return err
	}
	if err := poolconfig.ValidateEgress(c.Egress); err != nil {
		return fmt.Errorf("egress config egress: %w", err)
	}
	_, _, err := assemble(c, Options{})
	return err
}

func (c Config) validateLimits() error {
	switch {
	case c.MaxConns < 0 || c.IdleSeconds < 0 || c.LifetimeSeconds < 0:
		return errors.New("egress config limits must not be negative")
	case c.IdleSeconds > maxSeconds || c.LifetimeSeconds > maxSeconds:
		return fmt.Errorf("egress config timeouts must be at most %d seconds", maxSeconds)
	}
	return nil
}

func (c Config) policy() (*egress.Policy, error) {
	deny, err := c.denyPrefixes()
	if err != nil {
		return nil, err
	}
	return egress.New(c.Egress, deny)
}

func (c Config) denyPrefixes() ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(c.AlwaysDeny))
	for i, text := range c.AlwaysDeny {
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return nil, fmt.Errorf("egress config alwaysDeny %d is not a CIDR network", i)
		}
		out = append(out, prefix)
	}
	return out, nil
}
