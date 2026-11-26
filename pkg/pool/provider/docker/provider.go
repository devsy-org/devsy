package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

const (
	kindWorkspace = "workspace"
	kindNetwork   = "network"
	nounContainer = "container"
	idLength      = 64
	inspectBatch  = 100
)

// kind is one Docker object type the provider manages. Containers go before
// networks so a network is empty when it is removed.
type kind struct {
	name   string
	noun   string
	list   []string
	remove []string
}

var kinds = []kind{
	{
		kindWorkspace, nounContainer,
		[]string{nounContainer, "ls", "--all", "--quiet", "--no-trunc"},
		[]string{nounContainer, "rm", "--force", "--volumes"},
	},
	{
		kindNetwork, kindNetwork,
		[]string{kindNetwork, "ls", "--quiet", "--no-trunc"},
		[]string{kindNetwork, "rm"},
	},
}

// Provider manages session resources through a Runner.
//
// Docker labels cannot change after an object is created and an object ID is
// never reused, so an inspect followed by a remove by full ID cannot act on a
// relabelled or replaced object. That is why Delete needs no other guard.
type Provider struct {
	docker Runner
	// mu orders Spawn and Reclaim, so a rollback never removes what a
	// concurrent Spawn of the same session just created.
	mu sync.Mutex
}

var _ provider.Provider = (*Provider)(nil)

// New returns a Provider that runs docker commands through r.
func New(r Runner) *Provider {
	return &Provider{docker: r}
}

// scope narrows a search. Empty fields match anything.
type scope struct {
	pool    string
	session string
	id      string
}

func (s scope) filters() []string {
	var args []string
	add := func(filter string) { args = append(args, "--filter", filter) }
	if s.pool != "" {
		add("label=" + provider.LabelPool + "=" + s.pool)
	}
	if s.session != "" {
		add("label=" + provider.LabelSession + "=" + s.session)
	}
	if s.id != "" {
		add("id=" + s.id)
	}
	return args
}

func (s scope) matches(r provider.Resource) bool {
	return (s.pool == "" || r.Labels[provider.LabelPool] == s.pool) &&
		(s.session == "" || r.Labels[provider.LabelSession] == s.session) &&
		(s.id == "" || r.UID == s.id)
}

// List returns every container and network whose pool label equals pool. It
// fails if an object disappears between the listing and the inspect.
func (p *Provider) List(ctx context.Context, pool string) ([]provider.Resource, error) {
	if err := checkPool(ctx, pool); err != nil {
		return nil, err
	}
	return p.find(ctx, kinds, scope{pool: pool})
}

// Reclaim removes the pool's containers, then its networks, for one session.
func (p *Provider) Reclaim(ctx context.Context, pool, sessionID string) error {
	if err := checkPool(ctx, pool); err != nil {
		return err
	}
	if !session.OwnsID(pool, sessionID) {
		return provider.ErrInvalidSession
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reclaim(ctx, pool, sessionID)
}

// Delete removes one listed object when its live pool and session labels match.
func (p *Provider) Delete(ctx context.Context, pool string, r provider.Resource) error {
	if err := checkPool(ctx, pool); err != nil {
		return err
	}
	if r.UID == "" {
		return provider.ErrUIDRequired
	}
	k, ok := kindFor(r.Kind)
	if !ok || !validID(r.UID) {
		return nil
	}
	found, err := p.find(ctx, []kind{k}, scope{id: r.UID})
	if err != nil || len(found) == 0 || !unchanged(found[0], pool, r) {
		return err
	}
	return p.remove(ctx, k, r.UID)
}

// unchanged reports whether the live object is still in pool and still has
// the session label it was listed with.
func unchanged(live provider.Resource, pool string, listed provider.Resource) bool {
	return live.Labels[provider.LabelPool] == pool && sameSession(live.Labels, listed.Labels)
}

func (p *Provider) reclaim(ctx context.Context, pool, sessionID string) error {
	var errs []error
	for _, k := range kinds {
		found, err := p.find(ctx, []kind{k}, scope{pool: pool, session: sessionID})
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, r := range found {
			errs = append(errs, p.remove(ctx, k, r.UID))
		}
	}
	return errors.Join(errs...)
}

func checkPool(ctx context.Context, pool string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !poolconfig.ValidPoolName(pool) {
		return provider.ErrInvalidPool
	}
	return nil
}

func kindFor(name string) (kind, bool) {
	i := slices.IndexFunc(kinds, func(k kind) bool { return k.name == name })
	if i < 0 {
		return kind{}, false
	}
	return kinds[i], true
}

func sameSession(live, listed map[string]string) bool {
	a, aok := live[provider.LabelSession]
	b, bok := listed[provider.LabelSession]
	return aok == bok && a == b
}

// remove deletes by full ID. A failure is not an error when the object is
// already gone.
func (p *Provider) remove(ctx context.Context, k kind, id string) error {
	_, err := p.docker.Run(ctx, append(slices.Clone(k.remove), "--", id)...)
	if err == nil {
		return nil
	}
	if left, findErr := p.find(ctx, []kind{k}, scope{id: id}); findErr == nil && len(left) == 0 {
		return nil
	}
	return err
}

func (p *Provider) find(ctx context.Context, ks []kind, s scope) ([]provider.Resource, error) {
	var out []provider.Resource
	for _, k := range ks {
		ids, err := p.listIDs(ctx, k, s)
		if err != nil {
			return nil, err
		}
		found, err := p.inspect(ctx, k, ids)
		if err != nil {
			return nil, err
		}
		for _, r := range found {
			if s.matches(r) {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

func (p *Provider) listIDs(ctx context.Context, k kind, s scope) ([]string, error) {
	raw, err := p.docker.Run(ctx, append(slices.Clone(k.list), s.filters()...)...)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(raw))
	for _, id := range ids {
		if !validID(id) {
			return nil, fmt.Errorf("docker %s ls returned an unexpected ID", k.noun)
		}
	}
	return ids, nil
}

func (p *Provider) inspect(ctx context.Context, k kind, ids []string) ([]provider.Resource, error) {
	var out []provider.Resource
	for batch := range slices.Chunk(ids, inspectBatch) {
		args := append([]string{k.noun, "inspect", "--format", "{{json .}}", "--"}, batch...)
		raw, err := p.docker.Run(ctx, args...)
		if err != nil {
			return nil, err
		}
		found, err := decode(k, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

// inspected is the part of docker inspect output the provider reads. A
// container keeps its labels under Config, a network at the top level.
type inspected struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"`
	Labels map[string]string `json:"Labels"`
	Config inspectedConfig   `json:"Config"`
}

type inspectedConfig struct {
	Labels map[string]string `json:"Labels"`
}

func decode(k kind, raw []byte) ([]provider.Resource, error) {
	var out []provider.Resource
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var in inspected
		if err := json.Unmarshal([]byte(line), &in); err != nil {
			return nil, fmt.Errorf("docker %s inspect: %w", k.noun, err)
		}
		if !validID(in.ID) {
			return nil, fmt.Errorf("docker %s inspect returned an unexpected ID", k.noun)
		}
		labels := in.Labels
		if k.name == kindWorkspace {
			labels = in.Config.Labels
		}
		if labels == nil {
			labels = map[string]string{}
		}
		out = append(out, provider.Resource{
			Kind:   k.name,
			Name:   strings.TrimPrefix(in.Name, "/"),
			UID:    in.ID,
			Labels: labels,
		})
	}
	return out, nil
}

func validID(id string) bool {
	if len(id) != idLength {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
