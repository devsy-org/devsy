package docker

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/provider/providertest"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

const (
	neverStarted  = "0001-01-01T00:00:00Z"
	realDockerEnv = "DEVSY_POOL_DOCKER"
	// Docker hands out a few dozen network subnets, so the list spec seeds
	// fewer objects than it does against a simulated backend.
	realSeeded = 12
)

// TestContractAgainstRealDocker runs the provider contract on a Docker
// daemon. It creates and removes objects labelled for pools a, a-b, ab and b,
// so it refuses to start when any pool-labelled object already exists and
// fails when any is left behind.
func TestContractAgainstRealDocker(t *testing.T) {
	h := newRealDocker(t)
	providertest.RunContract(t, providertest.Harness{
		MaxSeeded: realSeeded,
		New: func(t *testing.T) provider.Provider {
			hook := &onceRunner{}
			t.Cleanup(func() { h.purge(t) })
			return &realProvider{Provider: New(hook), hook: hook}
		},
		Seed: func(t *testing.T, _ provider.Provider, r provider.Resource) { h.seed(t, r) },
		OnNextCall: func(_ *testing.T, p provider.Provider, fn func()) {
			p.(*realProvider).hook.set(fn)
		},
		Inventory: func(t *testing.T, _ provider.Provider) []provider.Resource {
			return h.inventory(t)
		},
	})
}

// TestStartRunsTheWorkspaceOnRealDocker checks what the contract cannot see:
// the container has run after Start, and not before.
func TestStartRunsTheWorkspaceOnRealDocker(t *testing.T) {
	h := newRealDocker(t)
	p := New(CLI{})
	id, err := session.NewID("a")
	if err != nil {
		t.Fatal(err)
	}
	spec := provider.Spec{Pool: "a", Session: id, Image: providertest.Image}
	if err := p.Spawn(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	name := resourcePrefix + id + "-workspace"
	if got := h.startedAt(t, name); got != neverStarted {
		t.Fatalf("container started by Spawn: %s", got)
	}
	for range 2 {
		if err := p.Start(t.Context(), "a", id); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.startedAt(t, name); got == neverStarted {
		t.Fatal("container did not start")
	}
}

// newRealDocker prepares the daemon and registers the check that the suite
// left nothing behind.
func newRealDocker(t *testing.T) *realDocker {
	t.Helper()
	if os.Getenv(realDockerEnv) != "1" {
		t.Skip("set " + realDockerEnv + "=1 to run against a Docker daemon")
	}
	h := &realDocker{}
	h.pull(t)
	h.requireClean(t)
	t.Cleanup(func() {
		h.purge(t)
		h.requireClean(t)
		if h.removed == 0 {
			t.Error("the suite created no object, so it proved nothing")
		}
	})
	return h
}

func (h *realDocker) startedAt(t *testing.T, name string) string {
	t.Helper()
	return h.must(t, nounContainer, verbInspect, flagFormat, "{{.State.StartedAt}}", "--", name)
}

type realProvider struct {
	*Provider
	hook *onceRunner
}

// onceRunner runs a function before the next docker command, which is how the
// contract cancels an operation from inside it.
type onceRunner struct {
	mu sync.Mutex
	fn func()
}

func (r *onceRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	fn := r.fn
	r.fn = nil
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return CLI{}.Run(ctx, args...)
}

func (r *onceRunner) set(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fn = fn
}

// realDocker drives the daemon directly, without the provider under test.
type realDocker struct {
	mu      sync.Mutex
	seeded  []string
	removed int
}

func (h *realDocker) run(t *testing.T, timeout time.Duration, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := CLI{}.Run(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

func (h *realDocker) must(t *testing.T, args ...string) string {
	t.Helper()
	out, err := h.run(t, time.Minute, args...)
	if err != nil {
		t.Fatalf("docker %s: %v", strings.Join(args[:2], " "), err)
	}
	return out
}

func (h *realDocker) pull(t *testing.T) {
	t.Helper()
	if _, err := h.run(t, time.Minute, "image", "inspect", "--", providertest.Image); err == nil {
		return
	}
	if _, err := h.run(t, 5*time.Minute, "pull", "--quiet", providertest.Image); err != nil {
		t.Fatalf("pull %s: %v", providertest.Image, err)
	}
}

func poolLabelled(r provider.Resource) bool {
	_, pool := r.Labels[provider.LabelPool]
	_, session := r.Labels[provider.LabelSession]
	return pool || session
}

func (h *realDocker) requireClean(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	seeded := len(h.seeded)
	h.mu.Unlock()
	var left []string
	for _, r := range h.inventory(t) {
		if poolLabelled(r) {
			left = append(left, r.Kind+" "+r.Name)
		}
	}
	if len(left) > 0 || seeded > 0 {
		t.Fatalf("pool-labelled objects exist (%d seeded): %v", seeded, left)
	}
}

type inventoryKind struct {
	kind, noun string
	all        bool
}

var inventoryKinds = []inventoryKind{
	{kindWorkspace, nounContainer, true},
	{kindNetwork, kindNetwork, false},
}

type inspectedObject struct {
	ID     string
	Name   string
	Labels map[string]string
	Config containerConfig
}

type containerConfig struct{ Labels map[string]string }

func (h *realDocker) inventory(t *testing.T) []provider.Resource {
	t.Helper()
	var out []provider.Resource
	for _, k := range inventoryKinds {
		args := []string{k.noun, "ls", flagQuiet, flagNoTrunc}
		if k.all {
			args = append(args, flagAll)
		}
		ids := strings.Fields(h.must(t, args...))
		if len(ids) == 0 {
			continue
		}
		raw := h.must(
			t,
			append([]string{k.noun, "inspect", flagFormat, "{{json .}}", "--"}, ids...)...)
		for line := range strings.SplitSeq(raw, "\n") {
			var in inspectedObject
			if err := json.Unmarshal([]byte(line), &in); err != nil {
				t.Fatalf("decode %s: %v", k.noun, err)
			}
			labels := in.Labels
			if k.kind == kindWorkspace {
				labels = in.Config.Labels
			}
			if labels == nil {
				labels = map[string]string{}
			}
			out = append(out, provider.Resource{
				Kind: k.kind, Name: strings.TrimPrefix(in.Name, "/"), UID: in.ID, Labels: labels,
			})
		}
	}
	return out
}

// seed replaces any object of that kind and name with one carrying exactly
// the labels in r.
func (h *realDocker) seed(t *testing.T, r provider.Resource) {
	t.Helper()
	noun := nounContainer
	remove := []string{"rm", flagForce, flagVolumes, "--", r.Name}
	create, operand := []string{"create", "--name", r.Name}, providertest.Image
	if r.Kind == kindNetwork {
		noun, remove = kindNetwork, []string{"rm", "--", r.Name}
		create, operand = []string{"create"}, r.Name
	}
	_, _ = h.run(t, time.Minute, append([]string{noun}, remove...)...)
	args := append([]string{noun}, create...)
	for k, v := range r.Labels {
		args = append(args, "--label", k+"="+v)
	}
	id := h.must(t, append(args, "--", operand)...)
	h.mu.Lock()
	h.seeded = append(h.seeded, noun+" "+id)
	h.mu.Unlock()
}

// purge removes every pool-labelled object and every seeded one, containers
// first.
func (h *realDocker) purge(t *testing.T) {
	t.Helper()
	ids := h.purgeTargets(t)
	for _, noun := range []string{nounContainer, kindNetwork} {
		if len(ids[noun]) == 0 {
			continue
		}
		args := []string{noun, "rm"}
		if noun == nounContainer {
			args = append(args, flagForce, flagVolumes)
		}
		if _, err := h.run(
			t,
			time.Minute,
			append(append(args, "--"), ids[noun]...)...); err != nil {
			t.Errorf("remove %ss: %v", noun, err)
		}
		h.mu.Lock()
		h.removed += len(ids[noun])
		h.mu.Unlock()
	}
}

func (h *realDocker) purgeTargets(t *testing.T) map[string][]string {
	t.Helper()
	ids := map[string][]string{}
	for _, r := range h.inventory(t) {
		if poolLabelled(r) {
			noun := nounContainer
			if r.Kind == kindNetwork {
				noun = kindNetwork
			}
			ids[noun] = append(ids[noun], r.UID)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.seeded {
		noun, id, _ := strings.Cut(s, " ")
		if !slices.Contains(ids[noun], id) {
			ids[noun] = append(ids[noun], id)
		}
	}
	h.seeded = nil
	return ids
}
