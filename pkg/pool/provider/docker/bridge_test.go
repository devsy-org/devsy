package docker

import (
	"context"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

func (s *ProviderSuite) TestOutboundBridgeNameCannotBeASessionNetworkName() {
	for _, pool := range []string{"a", "p-pool", "p-pool-abcdefghij", strings.Repeat("a", 32)} {
		name, ok := strings.CutPrefix(bridgeName(pool), resourcePrefix)
		s.True(ok)
		s.False(session.ValidID(name), "%s would also name a session network", name)
	}
}

func (s *ProviderSuite) TestEnsureBridgeRejectsSquattersAndMisnamedNetworks() {
	ctx := s.T().Context()
	for _, tc := range []struct {
		name string
		pool string
	}{
		{bridgeName("a"), ""},
		{bridgeName("a"), "b"},
		{"different-name", "a"},
	} {
		s.Run(tc.name+"/"+tc.pool, func() {
			d := &daemon{}
			var labels map[string]string
			if tc.pool != "" {
				labels = map[string]string{provider.LabelShared: tc.pool}
			}
			id := d.add(kindNetwork, tc.name, labels)
			d.objs[0].icc = iccDisabled
			p := fast(d)
			_, err := p.ensureBridge(ctx, "a")
			s.Require().ErrorContains(err, "refusing outbound bridge")
			s.True(d.has(id))
			for _, call := range d.commands() {
				s.Contains([]string{"ls", verbInspect}, call[1])
			}
		})
	}
}

func (s *ProviderSuite) TestReleaseBridgeRejectsWrongPoolLabel() {
	id := s.d.add(kindNetwork, bridgeName("a"), map[string]string{provider.LabelShared: "b"})
	s.d.objs[0].icc = iccDisabled
	s.ErrorContains(s.p.releaseBridge(s.T().Context(), "a"), "refusing outbound bridge")
	s.True(s.d.has(id))
}

func (s *ProviderSuite) TestEnsureBridgeVerifiesTheWinnerOfACreateRace() {
	for _, owner := range []string{"a", "b"} {
		s.Run(owner, func() {
			d := &daemon{}
			d.before = func(args []string) {
				if args[0] == kindNetwork && args[1] == verbCreate {
					d.add(
						kindNetwork,
						bridgeName("a"),
						map[string]string{provider.LabelShared: owner},
					)
					d.objs[0].icc = iccDisabled
				}
			}
			got, err := fast(d).ensureBridge(s.T().Context(), "a")
			if owner == "a" {
				s.Require().NoError(err)
				s.Equal(simulatedSubnet, got.prefix.String())
			} else {
				s.Require().ErrorContains(err, "refusing outbound bridge")
			}
			s.Equal(1, d.count())
		})
	}
}

func (s *ProviderSuite) TestEnsureBridgeMapsCapacityFailures() {
	ce := &CommandError{
		Stderr: "all predefined address pools have been fully subnetted",
		Err:    errInjected,
	}
	p := fast(runnerFunc(func(args []string) (string, error) {
		if args[1] == verbCreate {
			return "", ce
		}
		return "", nil
	}))
	_, err := p.ensureBridge(s.T().Context(), "a")
	s.ErrorIs(err, ErrNoAddressSpace)
	s.ErrorIs(err, errInjected)
}

func (s *ProviderSuite) TestASquatterOfTheBridgeNameDoesNotBlockReclaim() {
	id, err := session.NewID("a")
	s.Require().NoError(err)
	ws := s.d.add(kindWorkspace, "ws-"+id, labelsFor("a", id))
	net := s.d.add(kindNetwork, "net-"+id, labelsFor("a", id))
	squatter := s.d.add(kindNetwork, bridgeName("a"), nil)
	s.d.objs[len(s.d.objs)-1].icc = iccDisabled

	s.Require().NoError(s.p.Reclaim(s.T().Context(), "a", id))
	s.False(s.d.has(ws))
	s.False(s.d.has(net))
	s.True(s.d.has(squatter), "the squatter is left alone")
	s.ErrorContains(s.p.ReleaseShared(s.T().Context(), "a"), "refusing outbound bridge")
}

func (s *ProviderSuite) TestReleaseBridgeReportsOtherRemoveErrors() {
	ctx := s.T().Context()
	_, err := s.p.ensureBridge(ctx, "a")
	s.Require().NoError(err)
	s.d.rmErr = errInjected
	s.ErrorIs(s.p.releaseBridge(ctx, "a"), errInjected)
	s.Equal(1, s.d.count())
}

func (s *ProviderSuite) TestASidecarOnlyPoolKeepsTheBridge() {
	ctx := s.T().Context()
	_, err := s.p.ensureBridge(ctx, "a")
	s.Require().NoError(err)
	labels := labelsFor("a", "")
	labels[provider.LabelRole] = provider.RoleSidecar
	s.d.add(kindWorkspace, "sidecar-only", labels)

	s.Require().NoError(s.p.ReleaseShared(ctx, "a"))
	_, err = s.p.findBridge(ctx, "a")
	s.Require().NoError(err)
	s.Equal(2, s.d.count(), "bridge and sidecar both remain")
}

func (s *ProviderSuite) TestReleaseSharedWaitsForTheSamePoolOnly() {
	ctx := s.T().Context()
	unlock, err := s.p.lockBridge(ctx, "a")
	s.Require().NoError(err)
	defer unlock()
	s.NoError(within(func() error { return s.p.ReleaseShared(ctx, "b") }))
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	s.ErrorIs(s.p.ReleaseShared(short, "a"), context.DeadlineExceeded)
}
