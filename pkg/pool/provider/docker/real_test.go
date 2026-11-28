package docker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/provider/providertest"
	"github.com/devsy-org/devsy/pkg/pool/session"
	"github.com/stretchr/testify/suite"
)

const (
	neverStarted  = "0001-01-01T00:00:00Z"
	realDockerEnv = "DEVSY_POOL_DOCKER"
	// Docker hands out a few dozen network subnets, so the list spec seeds
	// fewer objects than it does against a simulated backend.
	realSeeded = 12
)

// TestContractAgainstRealDocker runs the provider contract on a Docker
// daemon. It refuses to start when pool-labelled objects exist.
func TestContractAgainstRealDocker(t *testing.T) {
	h := newRealDocker(t)
	providertest.RunContract(t, providertest.Harness{
		MaxSeeded: realSeeded,
		New: func(t *testing.T) provider.Provider {
			hook := &onceRunner{h: h}
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
		Workspace: func(t *testing.T, _ provider.Provider, _, id string) providertest.WorkspaceState {
			return h.workspaceState(t, id)
		},
	})
}

// TestRepeatStartLeavesARunningContainerAloneOnRealDocker covers what the
// contract cannot, because a bare image exits at once: Docker's start of a
// container that already runs keeps the same start time.
func TestRepeatStartLeavesARunningContainerAloneOnRealDocker(t *testing.T) {
	h := newRealDocker(t)
	pool := realPool(t)
	id, err := session.NewID(pool)
	if err != nil {
		t.Fatal(err)
	}
	h.must(t, nounContainer, verbCreate, "--name", resourcePrefix+id+"-workspace",
		"--label", provider.LabelPool+"="+pool, "--label", provider.LabelSession+"="+id,
		"--network", "none", "--", providertest.Image, "sleep", "300")
	p := New(&onceRunner{h: h})
	if err := p.Start(t.Context(), pool, id); err != nil {
		t.Fatal(err)
	}
	first := h.workspaceState(t, id)
	if !first.Running {
		t.Fatal("container is not running after Start")
	}
	if err := p.Start(t.Context(), pool, id); err != nil {
		t.Fatal(err)
	}
	if got := h.workspaceState(t, id); got != first {
		t.Fatalf("a repeat Start changed the container: %+v then %+v", first, got)
	}
}

// TestStartRunsTheWorkspaceOnRealDocker checks what the contract cannot see:
// the container has run after Start, and not before.
func TestStartRunsTheWorkspaceOnRealDocker(t *testing.T) {
	h := newRealDocker(t)
	p := New(&onceRunner{h: h})
	pool := realPool(t)
	id, err := session.NewID(pool)
	if err != nil {
		t.Fatal(err)
	}
	spec := provider.Spec{Pool: pool, Session: id, Image: providertest.Image}
	if err := p.Spawn(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	name := resourcePrefix + id + "-workspace"
	if got := h.startedAt(t, name); got != neverStarted {
		t.Fatalf("container started by Spawn: %s", got)
	}
	for range 2 {
		if err := p.Start(t.Context(), pool, id); err != nil {
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
	h := &realDocker{owned: map[string]bool{}}
	h.requireClean(t)
	h.pull(t)
	t.Cleanup(func() {
		h.purge(t)
		h.requireClean(t)
		if len(h.owned) == 0 {
			t.Error("the suite created no object, so it proved nothing")
		}
	})
	return h
}

func (h *realDocker) startedAt(t *testing.T, name string) string {
	t.Helper()
	return h.must(t, nounContainer, verbInspect, flagFormat, "{{.State.StartedAt}}", "--", name)
}

func (h *realDocker) workspaceState(t *testing.T, id string) providertest.WorkspaceState {
	t.Helper()
	out := h.must(t, nounContainer, verbInspect, flagFormat,
		"{{.State.Running}} {{.State.StartedAt}}", "--", resourcePrefix+id+"-workspace")
	running, began, _ := strings.Cut(out, " ")
	return providertest.WorkspaceState{Running: running == "true", Began: began}
}

type realProvider struct {
	*Provider
	hook *onceRunner
}

// onceRunner runs a function before the next docker command, which is how the
// contract cancels an operation from inside it.
type onceRunner struct {
	h  *realDocker
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
	out, err := CLI{}.Run(ctx, args...)
	r.h.trackCreate(args, string(out), err)
	return out, err
}

func (r *onceRunner) set(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fn = fn
}

// realDocker drives the daemon directly, without the provider under test.
type realDocker struct {
	mu    sync.Mutex
	owned map[string]bool
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
	h.trackCreate(args, out, err)
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

func (h *realDocker) trackCreate(args []string, out string, err error) {
	if err != nil || len(args) < 2 || args[1] != verbCreate {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.owned[strings.TrimSpace(out)] = true
}

func (h *realDocker) owns(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.owned[id]
}

func poolLabelled(r provider.Resource) bool {
	_, pool := r.Labels[provider.LabelPool]
	_, session := r.Labels[provider.LabelSession]
	_, shared := r.Labels[provider.LabelShared]
	return pool || session || shared
}

func (h *realDocker) requireClean(t *testing.T) {
	t.Helper()
	var left []string
	for _, r := range h.inventory(t) {
		if poolLabelled(r) || strings.Contains(r.Name, "p5a-test") {
			left = append(left, r.Kind+" "+r.Name)
		}
	}
	if len(left) > 0 {
		t.Fatalf("pool test or pool-labelled objects exist: %v", left)
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
			out = append(out, realResource(t, k, line))
		}
	}
	return out
}

func realResource(t *testing.T, k inventoryKind, line string) provider.Resource {
	t.Helper()
	var in inspected
	if err := json.Unmarshal([]byte(line), &in); err != nil {
		t.Fatalf("decode %s: %v", k.noun, err)
	}
	resourceKind, labels := k.kind, in.Labels
	if k.noun == nounContainer {
		labels = in.Config.Labels
		if labels[provider.LabelRole] == provider.RoleSidecar {
			resourceKind = kindSidecar
		}
	}
	if labels == nil {
		labels = map[string]string{}
	}
	return provider.Resource{
		Kind: resourceKind, Name: strings.TrimPrefix(in.Name, "/"), UID: in.ID, Labels: labels,
	}
}

func realPool(t *testing.T) string {
	t.Helper()
	id, err := session.NewID("p5a-test")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(id, "p-")
}

// seed replaces any object of that kind and name with one carrying exactly
// the labels in r.
func (h *realDocker) seed(t *testing.T, r provider.Resource) {
	t.Helper()
	noun := nounContainer
	create, operand := []string{
		verbCreate,
		"--name",
		r.Name,
		"--network",
		"none",
	}, providertest.Image
	if r.Kind == kindNetwork {
		noun = kindNetwork
		create, operand = []string{verbCreate}, r.Name
	}
	h.removeSeedTarget(t, r, noun)
	args := append([]string{noun}, create...)
	for k, v := range r.Labels {
		args = append(args, "--label", k+"="+v)
	}
	h.must(t, append(args, "--", operand)...)
}

func (h *realDocker) removeSeedTarget(t *testing.T, target provider.Resource, noun string) {
	t.Helper()
	for _, r := range h.inventory(t) {
		if r.Name != target.Name || (r.Kind == kindNetwork) != (noun == kindNetwork) {
			continue
		}
		if !h.owns(r.UID) {
			t.Fatalf("refusing to replace existing %s %s", noun, r.Name)
		}
		h.must(t, removeRealArgs(noun, []string{r.UID})...)
	}
}

func removeRealArgs(noun string, ids []string) []string {
	args := []string{noun, "rm"}
	if noun == nounContainer {
		args = append(args, flagForce, flagVolumes)
	}
	return append(append(args, "--"), ids...)
}

func (h *realDocker) purge(t *testing.T) {
	t.Helper()
	ids := h.purgeTargets(t)
	for _, noun := range []string{nounContainer, kindNetwork} {
		if len(ids[noun]) == 0 {
			continue
		}
		if _, err := h.run(t, time.Minute, removeRealArgs(noun, ids[noun])...); err != nil {
			t.Errorf("remove %ss: %v", noun, err)
		}
	}
}

func (h *realDocker) purgeTargets(t *testing.T) map[string][]string {
	t.Helper()
	ids := map[string][]string{}
	for _, r := range h.inventory(t) {
		if !h.owns(r.UID) {
			continue
		}
		noun := nounContainer
		if r.Kind == kindNetwork {
			noun = kindNetwork
			if _, shared := r.Labels[provider.LabelShared]; shared {
				t.Errorf("test leaked shared network %s", r.Name)
			}
		}
		ids[noun] = append(ids[noun], r.UID)
	}
	return ids
}

type RealBridgeSuite struct {
	suite.Suite
	h    *realDocker
	p    *Provider
	pool string
}

func TestRealBridgeSuite(t *testing.T) { suite.Run(t, new(RealBridgeSuite)) }

func (s *RealBridgeSuite) SetupTest() {
	s.h = newRealDocker(s.T())
	s.p = New(&onceRunner{h: s.h})
	s.pool = realPool(s.T())
}

func (s *RealBridgeSuite) TestEnsureCreatesAndReusesAnIsolatedOutboundBridge() {
	ctx := s.T().Context()
	first := s.ensure()
	s.Equal(driverNet, first.Driver)
	s.False(first.Internal)
	s.False(first.IPv6)
	s.Equal(iccDisabled, first.Options[optICC])
	s.Equal(map[string]string{provider.LabelShared: s.pool}, first.Labels)
	second := s.ensure()
	s.Equal(first.ID, second.ID)
	list, err := s.p.List(ctx, s.pool)
	s.Require().NoError(err)
	s.Empty(list)
	s.Require().NoError(s.p.releaseBridge(ctx, s.pool))
	s.bridgeIsGone()
}

func (s *RealBridgeSuite) TestEnsureAndReleaseRefuseAnUnlabelledSquatter() {
	id := s.h.must(
		s.T(),
		kindNetwork,
		verbCreate,
		"--opt",
		optICC+"="+iccDisabled,
		"--",
		bridgeName(s.pool),
	)
	s.refusesUnchanged(id)
	s.h.must(s.T(), kindNetwork, "rm", "--", id)
}

func (s *RealBridgeSuite) TestEnsureAndReleaseRefuseWrongBridgeOptions() {
	id := s.h.must(s.T(), kindNetwork, verbCreate, "--opt", optICC+"=true",
		"--label", provider.LabelShared+"="+s.pool, "--", bridgeName(s.pool))
	s.refusesUnchanged(id)
	s.h.must(s.T(), kindNetwork, "rm", "--", id)
}

func (s *RealBridgeSuite) TestReleaseKeepsABridgeWithAnActiveEndpoint() {
	ctx := s.T().Context()
	n := s.ensure()
	id := s.h.must(s.T(), nounContainer, verbCreate, "--name", s.pool+"-endpoint",
		"--network", n.ID, "--", testImage, "sleep", "300")
	s.h.must(s.T(), nounContainer, verbStart, "--", id)
	s.Require().NoError(s.p.releaseBridge(ctx, s.pool))
	s.Equal(n.ID, s.inspect().ID)
	s.h.must(s.T(), removeRealArgs(nounContainer, []string{id})...)
	s.Require().NoError(s.p.releaseBridge(ctx, s.pool))
	s.bridgeIsGone()
}

func (s *RealBridgeSuite) TestReclaimKeepsTheBridgeUntilTheLastSessionContainerIsRemoved() {
	ctx := s.T().Context()
	n := s.ensure()
	first, last := s.sessionID(), s.sessionID()
	s.Require().
		NoError(s.p.Spawn(ctx, provider.Spec{Pool: s.pool, Session: first, Image: testImage}))
	s.h.seed(s.T(), provider.Resource{
		Kind: kindSidecar, Name: s.pool + "-sidecar",
		Labels: map[string]string{
			provider.LabelPool:    s.pool,
			provider.LabelSession: last,
			provider.LabelRole:    provider.RoleSidecar,
		},
	})
	s.Require().NoError(s.p.Reclaim(ctx, s.pool, first))
	s.Equal(n.ID, s.inspect().ID)
	s.Require().NoError(s.p.Reclaim(ctx, s.pool, last))
	s.bridgeIsGone()
}

func (s *RealBridgeSuite) TestReleaseSharedRemovesABridgeWithoutSessions() {
	s.ensure()
	s.Require().NoError(s.p.ReleaseShared(s.T().Context(), s.pool))
	s.bridgeIsGone()
}

func (s *RealBridgeSuite) sessionID() string {
	s.T().Helper()
	id, err := session.NewID(s.pool)
	s.Require().NoError(err)
	return id
}

func (s *RealBridgeSuite) ensure() netInfo {
	s.T().Helper()
	got, err := s.p.ensureBridge(s.T().Context(), s.pool)
	s.Require().NoError(err)
	n := s.inspect()
	sub, err := n.subnet()
	s.Require().NoError(err)
	s.Equal(sub, got)
	return n
}

func (s *RealBridgeSuite) inspect() netInfo {
	s.T().Helper()
	raw := s.h.must(
		s.T(),
		kindNetwork,
		verbInspect,
		flagFormat,
		"{{json .}}",
		"--",
		bridgeName(s.pool),
	)
	n, err := parseNetwork([]byte(raw))
	s.Require().NoError(err)
	return n
}

func (s *RealBridgeSuite) bridgeIsGone() {
	s.T().Helper()
	s.Empty(
		s.h.must(s.T(), kindNetwork, "ls", flagQuiet, "--filter", "name=^"+bridgeName(s.pool)+"$"),
	)
}

func (s *RealBridgeSuite) refusesUnchanged(id string) {
	s.T().Helper()
	ctx := s.T().Context()
	before := s.inspect()
	s.Equal(id, before.ID)
	_, err := s.p.ensureBridge(ctx, s.pool)
	s.Require().ErrorContains(err, "refusing outbound bridge")
	s.Require().ErrorContains(s.p.releaseBridge(ctx, s.pool), "refusing outbound bridge")
	s.Equal(before, s.inspect())
}
