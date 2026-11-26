// Package provider defines the pool's runtime backend, not a Devsy workspace provider.
package provider

import (
	"context"
	"errors"
)

const (
	LabelPool    = "devsy.sh/pool"
	LabelSession = "devsy.sh/pool-session"
)

// ErrExists reports that a session already has resources.
var ErrExists = errors.New("session resources already exist")

// Resource is one runtime object. Kind is a short token such as workspace,
// network, or sidecar. Name is provider-native. Labels is a copy.
type Resource struct {
	Kind   string
	Name   string
	Labels map[string]string
}

// Spec requests a session spawn. Enforcement inputs will be added when an
// enforcer exists.
type Spec struct {
	Pool    string
	Session string
}

// Provider manages session resources. Every method honors context cancellation.
type Provider interface {
	// Spawn creates all resources for a session and labels each with LabelPool
	// and LabelSession. If the session already exists, it returns ErrExists
	// without changing anything.
	Spawn(ctx context.Context, spec Spec) error

	// Reclaim removes every resource of a session. No resources is success.
	Reclaim(ctx context.Context, pool, session string) error

	// List returns only resources with a LabelPool value exactly equal to pool.
	List(ctx context.Context, pool string) ([]Resource, error)

	// Delete removes exactly one listed resource. An absent resource is success.
	Delete(ctx context.Context, resource Resource) error
}
