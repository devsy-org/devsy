// Package providertest provides a fake and a contract suite for pool providers.
package providertest

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"sync"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
	"github.com/google/uuid"
)

// Method identifies a provider operation in the call log and fault controls.
type Method string

const (
	MethodSpawn   Method = "Spawn"
	MethodReclaim Method = "Reclaim"
	MethodList    Method = "List"
	MethodDelete  Method = "Delete"

	networkKind   = "network"
	workspaceKind = "workspace"
)

// Call records one invocation. Delete records the resource and its label values.
type Call struct {
	Method   Method
	Pool     string
	Session  string
	Resource provider.Resource
}

// Fake is a concurrency-safe, in-memory pool provider. Its zero value is ready to use.
type Fake struct {
	mu        sync.Mutex
	resources map[resourceKey]provider.Resource
	calls     []Call
	next      map[Method]error
	always    map[Method]error
	partial   error
	hook      func(context.Context, Call)
}

type resourceKey struct{ kind, name string }

var (
	_ provider.Provider = (*Fake)(nil)

	errNameUnavailable = errors.New("resource name is unavailable")
)

// FailNext sets a one-shot error for method. A nil error clears it.
func (f *Fake) FailNext(method Method, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next == nil {
		f.next = make(map[Method]error)
	}
	f.next[method] = err
}

// SetError sets a persistent error for method. A nil error clears it.
func (f *Fake) SetError(method Method, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.always == nil {
		f.always = make(map[Method]error)
	}
	f.always[method] = err
}

// FailNextSpawnAfterWorkspace makes the next spawn that creates resources
// return err after creating only its workspace. A nil error clears it.
func (f *Fake) FailNextSpawnAfterWorkspace(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partial = err
}

// SetHook sets a hook that runs after recording a call and before acting on it.
// The mutex is not held, so the hook can block or inspect the fake.
func (f *Fake) SetHook(hook func(context.Context, Call)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hook = hook
}

// Calls returns a copy of recorded calls in entry order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := slices.Clone(f.calls)
	for i := range calls {
		calls[i].Resource = copyResource(calls[i].Resource)
	}
	return calls
}

// Add seeds a resource, replacing any resource with the same kind and name.
// It assigns a UID if the caller does not supply one.
func (f *Fake) Add(r provider.Resource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addLocked(copyResource(r))
}

// Snapshot returns a copy of all resources sorted by kind and name.
func (f *Fake) Snapshot() []provider.Resource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.collect(func(provider.Resource) bool { return true })
}

// Spawn implements provider.Provider.
func (f *Fake) Spawn(ctx context.Context, spec provider.Spec) error {
	call := Call{Method: MethodSpawn, Pool: spec.Pool, Session: spec.Session}
	return f.run(ctx, call, func() error { return f.spawnLocked(spec) })
}

// Reclaim implements provider.Provider.
func (f *Fake) Reclaim(ctx context.Context, pool, id string) error {
	call := Call{Method: MethodReclaim, Pool: pool, Session: id}
	return f.run(ctx, call, func() error {
		if !session.OwnsID(pool, id) {
			return provider.ErrInvalidSession
		}
		for key, r := range f.resources {
			if belongsTo(r, pool, id) {
				delete(f.resources, key)
			}
		}
		return nil
	})
}

// List implements provider.Provider.
func (f *Fake) List(ctx context.Context, pool string) ([]provider.Resource, error) {
	var resources []provider.Resource
	err := f.run(ctx, Call{Method: MethodList, Pool: pool}, func() error {
		resources = f.collect(func(r provider.Resource) bool { return inPool(r, pool) })
		return nil
	})
	return resources, err
}

// Delete implements provider.Provider.
func (f *Fake) Delete(ctx context.Context, pool string, resource provider.Resource) error {
	call := Call{
		Method:   MethodDelete,
		Pool:     pool,
		Session:  resource.Labels[provider.LabelSession],
		Resource: resource,
	}
	return f.run(ctx, call, func() error {
		if resource.UID == "" {
			return provider.ErrUIDRequired
		}
		key := keyFor(resource)
		if live, ok := f.resources[key]; ok && live.UID == resource.UID &&
			inPool(live, pool) && sameSession(live, resource) {
			delete(f.resources, key)
		}
		return nil
	})
}

func (f *Fake) run(ctx context.Context, call Call, act func() error) error {
	if !poolconfig.ValidPoolName(call.Pool) {
		return provider.ErrInvalidPool
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if hook := f.record(call); hook != nil {
		call.Resource = copyResource(call.Resource)
		hook(ctx, call)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.failure(call.Method); err != nil {
		return err
	}
	return act()
}

func (f *Fake) record(call Call) func(context.Context, Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call.Resource = copyResource(call.Resource)
	f.calls = append(f.calls, call)
	return f.hook
}

func (f *Fake) failure(method Method) error {
	if err := f.next[method]; err != nil {
		delete(f.next, method)
		return err
	}
	return f.always[method]
}

func (f *Fake) spawnLocked(spec provider.Spec) error {
	if !session.OwnsID(spec.Pool, spec.Session) {
		return provider.ErrInvalidSession
	}
	for _, r := range f.resources {
		if belongsTo(r, spec.Pool, spec.Session) {
			return provider.ErrExists
		}
	}
	workspace := provider.Resource{
		Kind: workspaceKind,
		Name: spec.Session,
		Labels: map[string]string{
			provider.LabelPool:    spec.Pool,
			provider.LabelSession: spec.Session,
		},
	}
	network := workspace
	network.Kind = networkKind
	if f.nameTaken(workspace) || f.nameTaken(network) {
		return errNameUnavailable
	}
	f.addLocked(workspace)
	if err := f.partial; err != nil {
		f.partial = nil
		return err
	}
	f.addLocked(network)
	return nil
}

func (f *Fake) nameTaken(r provider.Resource) bool {
	_, ok := f.resources[keyFor(r)]
	return ok
}

func (f *Fake) addLocked(r provider.Resource) {
	if f.resources == nil {
		f.resources = make(map[resourceKey]provider.Resource)
	}
	if r.UID == "" {
		r.UID = uuid.NewString()
	}
	f.resources[keyFor(r)] = r
}

func (f *Fake) collect(keep func(provider.Resource) bool) []provider.Resource {
	var resources []provider.Resource
	for _, r := range f.resources {
		if keep(r) {
			resources = append(resources, copyResource(r))
		}
	}
	slices.SortFunc(resources, func(a, b provider.Resource) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
	return resources
}

func inPool(r provider.Resource, pool string) bool {
	label, ok := r.Labels[provider.LabelPool]
	return ok && label == pool
}

func sameSession(a, b provider.Resource) bool {
	left, leftOK := a.Labels[provider.LabelSession]
	right, rightOK := b.Labels[provider.LabelSession]
	return leftOK == rightOK && left == right
}

func belongsTo(r provider.Resource, pool, id string) bool {
	label, ok := r.Labels[provider.LabelSession]
	return inPool(r, pool) && ok && label == id
}

func keyFor(r provider.Resource) resourceKey {
	return resourceKey{kind: r.Kind, name: r.Name}
}

func copyResource(r provider.Resource) provider.Resource {
	r.Labels = maps.Clone(r.Labels)
	return r
}
