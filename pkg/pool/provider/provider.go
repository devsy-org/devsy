// Package provider defines the pool's runtime backend, not a Devsy workspace provider.
package provider

import (
	"context"
	"errors"
)

const (
	LabelPool    = "devsy.sh/pool"
	LabelSession = "devsy.sh/pool-session"
	LabelShared  = "devsy.sh/pool-shared"
)

// ErrExists reports that a session already has resources.
var ErrExists = errors.New("session resources already exist")

// ErrInvalidPool reports that a pool name fails poolconfig.ValidPoolName.
var ErrInvalidPool = errors.New("pool name is not valid")

// ErrInvalidImage reports that Spec.Image is empty, starts with "-" or holds
// whitespace or control characters. Spawn returns it without changing anything.
var ErrInvalidImage = errors.New("image reference is not valid")

// ErrInvalidSession reports that a session ID is not owned by the pool.
var ErrInvalidSession = errors.New("session ID does not belong to pool")

// ErrNotFound reports that Start found no workspace for the session.
var ErrNotFound = errors.New("session has no workspace")

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

// Spec requests a session spawn. Image is the workspace image reference;
// CheckImage defines what a provider accepts. Enforcement inputs will be added
// when an enforcer exists.
type Spec struct {
	Pool    string
	Session string
	Image   string
}

// Provider manages session resources. Every method honors context cancellation
// and rejects pool names that fail poolconfig.ValidPoolName with ErrInvalidPool
// without changing resources. A real provider must also return promptly once
// its context is canceled, including from a blocked network call: the engine's
// Reclaim holds a lock across its provider calls, so a call that ignores the
// context stalls every other session operation on that Engine.
//
// Spawn is the one exception. A create command that has started runs to its
// end, up to 30 seconds, because a killed command can still finish in the
// daemon unseen. An image the daemon lacks is pulled first, up to 5 minutes;
// a pull creates nothing, so a cancel stops it at once. The rollback after a failure or cancel can take up to 30
// seconds more. A canceled Spawn can therefore return up to 60 seconds late.
type Provider interface {
	// Spawn creates all resources for a session and labels each with LabelPool
	// and LabelSession. If the session already exists, it returns ErrExists
	// without changing anything. If session.OwnsID rejects the session ID,
	// Spawn returns ErrInvalidSession, and if CheckImage rejects Spec.Image it
	// returns ErrInvalidImage, in both cases without changing anything. When
	// Spawn fails or is canceled after creating some resources, it removes them
	// again before it returns. A cancel does not kill a create command that
	// has started, because a killed docker CLI can still finish in the daemon
	// unseen, so Spawn may return up to a create timeout after the cancel.
	Spawn(ctx context.Context, spec Spec) error

	// Start starts the session's workspace. It is idempotent: a workspace
	// that is already started stays started and Start returns nil. If
	// session.OwnsID rejects the session ID, Start returns ErrInvalidSession,
	// and if the session has no workspace it returns ErrNotFound, in both
	// cases without changing anything. Start creates and removes no resource.
	// A canceled Start may leave the workspace started or not; the caller
	// decides by listing or by Reclaim.
	Start(ctx context.Context, pool, session string) error

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
	// LabelPool equal to pool, Delete does nothing and returns nil. It also
	// does nothing when the live object's LabelSession (present or absent)
	// differs from the one on resource, so an object relabelled after it was
	// listed is never deleted on a stale decision. A backend must guard
	// against a change to either label between that comparison and the
	// deletion; a UID precondition alone cannot detect a relabel.
	// Where the backend supports a delete precondition (such as Kubernetes
	// UID preconditions), the implementation must use it. If pool labels can
	// change, the delete must also guard against a change after the label check
	// (for example, with a Kubernetes resourceVersion precondition). Without
	// backend preconditions, the check and delete must be atomic and object
	// names must never be reused across pools.
	Delete(ctx context.Context, pool string, resource Resource) error
}
