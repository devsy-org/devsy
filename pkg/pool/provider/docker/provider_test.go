package docker

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
	"github.com/stretchr/testify/suite"
)

type ProviderSuite struct {
	suite.Suite
	d *daemon
	p *Provider
}

func TestProviderSuite(t *testing.T) { suite.Run(t, new(ProviderSuite)) }

func (s *ProviderSuite) SetupTest() {
	s.d = &daemon{}
	s.p = fast(s.d)
}

func (s *ProviderSuite) TestListReturnsOnlyTheExactPool() {
	id := s.sessionID("a")
	c, n := s.spawnLike("a", id)
	s.spawnLike("a-b", s.sessionID("a-b"))
	s.d.add(kindNetwork, "unlabelled", nil)
	s.d.add(kindNetwork, "session-only", map[string]string{provider.LabelSession: id})
	orphan := s.d.add(kindNetwork, "orphan", labelsFor("a", ""))
	got := s.list()
	s.Require().Len(got, 3)
	uids := []string{}
	for _, r := range got {
		uids = append(uids, r.UID)
		s.Equal("a", r.Labels[provider.LabelPool])
	}
	s.ElementsMatch([]string{c, n, orphan}, uids)
	byUID := map[string]provider.Resource{}
	for _, r := range got {
		byUID[r.UID] = r
	}
	s.Equal(provider.Resource{
		Kind: kindWorkspace, Name: "ws-" + id, UID: c, Labels: labelsFor("a", id),
	}, byUID[c])
	s.Equal(kindNetwork, byUID[n].Kind)
	s.Equal(kindNetwork, byUID[orphan].Kind)
	s.NotContains(byUID[orphan].Labels, provider.LabelSession)
}

func (s *ProviderSuite) TestListChecksLabelsAgainBecauseTheFilterIsAHint() {
	s.spawnLike("a", s.sessionID("a"))
	s.spawnLike("a-b", s.sessionID("a-b"))
	s.d.loose = true
	s.Len(s.list(), 2)
}

func (s *ProviderSuite) TestListReturnsLabelCopies() {
	s.spawnLike("a", s.sessionID("a"))
	first := s.list()
	delete(first[0].Labels, provider.LabelPool)
	s.Len(s.list()[0].Labels, 2)
}

func (s *ProviderSuite) TestListInspectsInBatches() {
	for range 120 {
		s.d.add(kindNetwork, "n", labelsFor("a", ""))
	}
	s.Len(s.list(), 120)
	var sizes []int
	for _, c := range s.d.commands() {
		if c[0] == kindNetwork && c[1] == verbInspect {
			sizes = append(sizes, len(c)-5)
		}
	}
	s.Equal([]int{100, 20}, sizes)
}

func (s *ProviderSuite) TestListFailsOnOutputItDoesNotUnderstand() {
	for name, run := range map[string]runnerFunc{
		"id":        func([]string) (string, error) { return "not-an-id\n", nil },
		verbInspect: func(a []string) (string, error) { return inspectOrIDs(a, "{not json}\n"), nil },
		"inspect id": func(a []string) (string, error) {
			return inspectOrIDs(a, `{"Id":"x","Name":"n"}`+"\n"), nil
		},
		"command": func([]string) (string, error) { return "", errNoSuchObject },
	} {
		s.Run(name, func() {
			_, err := New(run).List(s.T().Context(), "a")
			s.Error(err)
		})
	}
}

type runnerFunc func(args []string) (string, error)

func (f runnerFunc) Run(_ context.Context, args ...string) ([]byte, error) {
	out, err := f(args)
	return []byte(out), err
}

func inspectOrIDs(args []string, inspect string) string {
	if args[1] == verbInspect {
		return inspect
	}
	return fmt.Sprintf("%064d\n", 7)
}

func (s *ProviderSuite) TestDeleteRemovesTheListedObject() {
	c, n := s.spawnLike("a", s.sessionID("a"))
	for _, r := range s.list() {
		s.Require().NoError(s.p.Delete(s.T().Context(), "a", r))
	}
	s.False(s.d.has(c))
	s.False(s.d.has(n))
	for _, r := range s.list() {
		s.Failf("left behind", "%v", r)
	}
}

func (s *ProviderSuite) TestDeleteDoesNothingWhenTheLiveObjectDiffers() {
	id := s.sessionID("a")
	c, _ := s.spawnLike("a", id)
	s.spawnLike("a-b", s.sessionID("a-b"))
	listed := s.list()
	replaced := s.d.replace(c)
	other := s.d.add(kindNetwork, "relabelled", labelsFor("a", id))
	s.d.relabel(other, provider.LabelSession, s.sessionID("a"))
	forged := provider.Resource{
		Kind: kindNetwork, UID: other, Labels: labelsFor("a", id),
	}
	for _, r := range append(listed, forged) {
		s.Require().NoError(s.p.Delete(s.T().Context(), "a", r))
	}
	s.True(s.d.has(replaced))
	s.True(s.d.has(other))
	s.Require().NoError(s.p.Delete(s.T().Context(), "a-b", provider.Resource{
		Kind: kindNetwork, UID: other, Labels: labelsFor("a-b", id),
	}))
	s.True(s.d.has(other))
}

func (s *ProviderSuite) TestDeleteDoesNothingForAnObjectOfAnotherPoolWithTheSameSession() {
	victimID := s.sessionID("a-b")
	victim := s.d.add(kindNetwork, "victim", labelsFor("a-b", victimID))
	forged := provider.Resource{Kind: kindNetwork, UID: victim, Labels: labelsFor("a", victimID)}
	s.Require().NoError(s.p.Delete(s.T().Context(), "a", forged))
	s.True(s.d.has(victim))
}

func (s *ProviderSuite) TestDeleteTellsAMissingSessionLabelFromAnEmptyOne() {
	empty := s.d.add(kindNetwork, "empty", labelsFor("a", ""))
	s.d.relabel(empty, provider.LabelSession, "")
	r := provider.Resource{Kind: kindNetwork, UID: empty, Labels: labelsFor("a", "")}
	s.Require().NoError(s.p.Delete(s.T().Context(), "a", r))
	s.True(s.d.has(empty), "listed without a session label, live label is empty")
	r.Labels[provider.LabelSession] = ""
	s.Require().NoError(s.p.Delete(s.T().Context(), "a", r))
	s.False(s.d.has(empty))
}

func (s *ProviderSuite) TestDeleteSendsNothingForAnUnusableUID() {
	s.spawnLike("a", s.sessionID("a"))
	good := s.list()[0]
	before := len(s.d.commands())
	for _, uid := range []string{
		flagAll, "-f", "ws-name", "abc", shellBait, "x; rm -rf /",
		good.UID[:12], string([]byte(good.UID)[:63]) + "G", good.UID + "0",
		strings.ToUpper(good.UID),
	} {
		r := good
		r.UID = uid
		s.Require().NoError(s.p.Delete(s.T().Context(), "a", r), uid)
	}
	bad := good
	bad.Kind = "volume"
	s.Require().NoError(s.p.Delete(s.T().Context(), "a", bad))
	s.Len(s.d.commands(), before, "no docker command ran")
	s.Equal(2, s.d.count())
	good.UID = ""
	s.ErrorIs(s.p.Delete(s.T().Context(), "a", good), provider.ErrUIDRequired)
}

func (s *ProviderSuite) TestDeleteToleratesAnObjectThatVanishedButReportsOtherFailures() {
	s.spawnLike("a", s.sessionID("a"))
	listed := s.list()
	s.d.before = func(args []string) {
		if args[1] == "rm" {
			s.d.mu.Lock()
			s.d.objs = nil
			s.d.mu.Unlock()
		}
	}
	s.Require().NoError(s.p.Delete(s.T().Context(), "a", listed[0]))

	s.d.before = nil
	boom := errors.New("daemon refused")
	s.d.rmErr = boom
	s.spawnLike("a", s.sessionID("a"))
	for _, r := range s.list() {
		s.ErrorIs(s.p.Delete(s.T().Context(), "a", r), boom)
	}
}

func (s *ProviderSuite) TestDeleteReportsTheRemoveErrorWhenTheLookupAfterItFailsToo() {
	c, _ := s.spawnLike("a", s.sessionID("a"))
	listed := s.list()
	boom := errors.New("daemon refused")
	s.d.rmErr = boom
	// Delete runs ls, inspect, rm, then the confirming ls.
	s.d.failAt = len(s.d.commands()) + 4
	for _, r := range listed {
		if r.UID == c {
			s.ErrorIs(s.p.Delete(s.T().Context(), "a", r), boom)
		}
	}
	s.True(s.d.has(c), "the object was never removed, so nil would hide a leak")
}

func (s *ProviderSuite) TestReclaimRemovesOneSessionContainersFirst() {
	id := s.sessionID("a")
	s.spawnLike("a", id)
	keepA, keepB := s.spawnLike("a", s.sessionID("a"))
	foreign := s.d.add(kindNetwork, "foreign", labelsFor("a-b", id))
	s.Require().NoError(s.p.Reclaim(s.T().Context(), "a", id))
	s.Equal(3, s.d.count(), "the other session and the foreign network stay")
	s.True(s.d.has(keepA))
	s.True(s.d.has(keepB))
	s.True(s.d.has(foreign))
	var removed []string
	for _, c := range s.d.commands() {
		if c[1] == "rm" {
			removed = append(removed, c[0])
		}
	}
	s.Equal([]string{"container", "network"}, removed)
	s.Require().NoError(s.p.Reclaim(s.T().Context(), "a", id))
}

func (s *ProviderSuite) TestReclaimKeepsGoingAndReportsEveryFailure() {
	id := s.sessionID("a")
	s.spawnLike("a", id)
	boom := errors.New("daemon refused")
	s.d.rmErr = boom
	err := s.p.Reclaim(s.T().Context(), "a", id)
	s.ErrorIs(err, boom)
	s.Equal(2, s.d.count())
	rms := 0
	for _, c := range s.d.commands() {
		if c[1] == "rm" {
			rms++
		}
	}
	s.Equal(2, rms)
}

func (s *ProviderSuite) TestInvalidInputsRunNoCommand() {
	ctx := s.T().Context()
	good := s.sessionID("a")
	for _, pool := range []string{"", "A", "a_b", spaced, flagAll, "a\n"} {
		_, err := s.p.List(ctx, pool)
		s.ErrorIs(err, provider.ErrInvalidPool, pool)
		s.ErrorIs(s.p.Reclaim(ctx, pool, good), provider.ErrInvalidPool, pool)
		s.ErrorIs(s.p.Delete(ctx, pool, provider.Resource{Kind: kindNetwork, UID: "x"}),
			provider.ErrInvalidPool, pool)
	}
	for _, id := range []string{"", "invalid", s.sessionID("a-b"), good + " --all", good + "x"} {
		s.ErrorIs(s.p.Reclaim(ctx, "a", id), provider.ErrInvalidSession, id)
	}
	s.Empty(s.d.commands())
}

func (s *ProviderSuite) TestCanceledContextRunsNoCommand() {
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()
	_, err := s.p.List(ctx, "a")
	s.ErrorIs(err, context.Canceled)
	s.ErrorIs(s.p.Reclaim(ctx, "a", s.sessionID("a")), context.Canceled)
	gone := provider.Resource{Kind: kindNetwork, UID: "x"}
	s.ErrorIs(s.p.Delete(ctx, "a", gone), context.Canceled)
	s.Empty(s.d.commands())
}

func (s *ProviderSuite) TestCancelInsideACommandStopsTheOperation() {
	id := s.sessionID("a")
	s.spawnLike("a", id)
	listed := s.list()
	for _, verb := range []string{"ls", verbInspect, "rm"} {
		s.Run(verb, func() {
			ctx, cancel := context.WithCancel(s.T().Context())
			defer cancel()
			s.d.before = func(args []string) {
				if args[1] == verb {
					cancel()
				}
			}
			var err error
			if verb == "rm" {
				err = s.p.Reclaim(ctx, "a", id)
			} else {
				_, err = s.p.List(ctx, "a")
			}
			s.ErrorIs(err, context.Canceled)
			s.Equal(2, s.d.count())
		})
	}
	s.d.before = nil
	s.Len(s.list(), len(listed))
}

var safeValue = regexp.MustCompile(
	`^(label=devsy\.sh/pool(-session)?=[a-z0-9-]+|id=[0-9a-f]{64}|\{\{json \.\}\})$`,
)

// TestEveryArgumentIsAFlagAFilterOrAnIDAfterTheTerminator checks the shape of
// every command a hostile mix of inputs produced.
func (s *ProviderSuite) TestEveryArgumentIsAFlagAFilterOrAnIDAfterTheTerminator() {
	ctx := s.T().Context()
	id := s.sessionID("a")
	s.spawnLike("a", id)
	live := s.list()[0].UID
	for _, pool := range []string{"a", flagAll, "a;id", spaced, shellBait} {
		_, _ = s.p.List(ctx, pool)
		_ = s.p.Reclaim(ctx, pool, id)
	}
	for _, uid := range []string{flagForce, shellBait, live} {
		_ = s.p.Delete(ctx, "a", provider.Resource{Kind: kindWorkspace, UID: uid})
	}
	_ = s.p.Reclaim(ctx, "a", id+" --all")
	flags := []string{
		"container", "network", "ls", "inspect", "rm", "--all", flagQuiet, flagNoTrunc,
		flagForce, flagVolumes, flagFormat, "--filter", "--",
	}
	for _, c := range s.d.commands() {
		s.checkArgv(c, flags)
	}
	s.NotEmpty(s.d.commands())
}

func (s *ProviderSuite) checkArgv(c, flags []string) {
	terminated := false
	for _, a := range c {
		switch {
		case terminated:
			s.Regexp(`^[0-9a-f]{64}$`, a, "%v", c)
		case a == "--":
			terminated = true
		case slices.Contains(flags, a), safeValue.MatchString(a):
		default:
			s.Failf("unexpected argument", "%q in %v", a, c)
		}
	}
}

func (s *ProviderSuite) sessionID(pool string) string {
	s.T().Helper()
	id, err := session.NewID(pool)
	s.Require().NoError(err)
	return id
}

func (s *ProviderSuite) spawnLike(pool, id string) (container, network string) {
	container = s.d.add(kindWorkspace, "ws-"+id, labelsFor(pool, id))
	network = s.d.add(kindNetwork, "net-"+id, labelsFor(pool, id))
	return container, network
}

func (s *ProviderSuite) list() []provider.Resource {
	s.T().Helper()
	got, err := s.p.List(s.T().Context(), "a")
	s.Require().NoError(err)
	return got
}
