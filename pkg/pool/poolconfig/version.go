package poolconfig

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// policy is the part of a pool that governs what its workspaces run and can
// reach. Capacity, lifecycle, audit settings and the pool name stay out, so
// tuning them keeps the policy version.
type policy struct {
	APIVersion  string           `json:"apiVersion"`
	Kind        string           `json:"kind"`
	Source      Source           `json:"source"`
	Provider    Provider         `json:"provider"`
	Egress      Egress           `json:"egress"`
	Credentials []Credential     `json:"credentials,omitempty"`
	Agents      map[string]Agent `json:"agents"`
}

// PolicyVersion hashes the rules that govern a workspace. Files that differ
// only in formatting, key order, spelled-out defaults or the order of
// set-like lists share a version.
func (p *Pool) PolicyVersion() (string, error) {
	n := p.normalized()
	return hashJSON(policy{
		APIVersion:  n.APIVersion,
		Kind:        n.Kind,
		Source:      n.Spec.Source,
		Provider:    n.Spec.Provider,
		Egress:      n.Spec.Egress,
		Credentials: n.Spec.Credentials,
		Agents:      n.Spec.Agents,
	})
}

// ConfigVersion hashes the whole normalized pool, so a change that leaves the
// policy version alone, such as a new audit directory, is still visible.
func (p *Pool) ConfigVersion() (string, error) {
	n := p.normalized()
	return hashJSON(&n)
}

func hashJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode pool config: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// normalized copies the pool with ports, delivery targets, repositories and
// agent credentials sorted and deduplicated. Validation already refuses
// duplicates; this keeps the hash stable if a caller skips it. Command
// arguments and allow rules keep their order.
func (p *Pool) normalized() Pool {
	n := *p
	n.Spec.Egress.Allow = slices.Clone(p.Spec.Egress.Allow)
	for i := range n.Spec.Egress.Allow {
		n.Spec.Egress.Allow[i].Ports = sortedSet(n.Spec.Egress.Allow[i].Ports)
	}
	n.Spec.Credentials = slices.Clone(p.Spec.Credentials)
	for i := range n.Spec.Credentials {
		c := &n.Spec.Credentials[i]
		c.Deliver = sortedSet(c.Deliver)
		if c.GitHubApp != nil && c.GitHubApp.Scope != nil {
			app, scope := *c.GitHubApp, *c.GitHubApp.Scope
			scope.Repositories = sortedSet(scope.Repositories)
			app.Scope = &scope
			c.GitHubApp = &app
		}
	}
	if p.Spec.Agents != nil {
		n.Spec.Agents = maps.Clone(p.Spec.Agents)
		for name, agent := range n.Spec.Agents {
			agent.Credentials = sortedSet(agent.Credentials)
			n.Spec.Agents[name] = agent
		}
	}
	return n
}

func sortedSet[T cmp.Ordered](items []T) []T {
	out := slices.Clone(items)
	slices.Sort(out)
	return slices.Compact(out)
}
