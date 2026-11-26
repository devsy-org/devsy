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

// ErrInvalidPool reports that a pool name fails poolconfig.ValidPoolName.
var ErrInvalidPool = errors.New("pool name is not valid")

// ErrInvalidSession reports that a session ID is not owned by the pool.
var ErrInvalidSession = errors.New("session ID does not belong to pool")

// ErrUIDRequired reports that Delete was called without a resource UID.
var ErrUIDRequired = errors.New("resource UID is required")

// Resource is one runtime object. Kind is a short token such as workspace,
// network, or sidecar. Name is provider-native. UID is the opaque identity
// or generation assigned to the live object by the backend. Labels is a copy.
type Resource struct {
	Kind   string
	Name   string
	UID    string
	Labels map[string]string
}

// Spec requests a session spawn. Enforcement inputs will be added when an
// enforcer exists.
type Spec struct {
	Pool    string
	Session string
}

// Provider manages session resources. Every method honors context cancellation
// and rejects pool names that fail poolconfig.ValidPoolName with ErrInvalidPool
// without changing resources.
type Provider interface {
	// Spawn creates all resources for a session and labels each with LabelPool
	// and LabelSession. If the session already exists, it returns ErrExists
	// without changing anything. If session.OwnsID rejects the session ID,
	// Spawn returns ErrInvalidSession without changing anything.
	Spawn(ctx context.Context, spec Spec) error

	// Reclaim removes every resource with both LabelPool and LabelSession
	// exactly matching the pool and session ID. No resources is success.
	// If session.OwnsID rejects the session ID, Reclaim returns
	// ErrInvalidSession without changing anything.
	Reclaim(ctx context.Context, pool, session string) error

	// List returns all and only resources with a LabelPool value exactly
	// equal to pool, each with its live UID and a copy of its labels.
	List(ctx context.Context, pool string) ([]Resource, error)

	// Delete removes exactly one listed resource. An empty UID returns
	// ErrUIDRequired without changing anything. The caller's labels are not
	// trusted. If the live object is absent, has a different UID, or lacks
	// LabelPool equal to pool, Delete does nothing and returns nil.
	// Where the backend supports a delete precondition (such as Kubernetes
	// UID preconditions), the implementation must use it. If pool labels can
	// change, the delete must also guard against a change after the label check
	// (for example, with a Kubernetes resourceVersion precondition). Without
	// backend preconditions, the check and delete must be atomic and object
	// names must never be reused across pools.
	Delete(ctx context.Context, pool string, resource Resource) error
}
