package docker

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/stretchr/testify/suite"
)

type NetplanSuite struct {
	suite.Suite
}

func TestNetplanSuite(t *testing.T) {
	suite.Run(t, new(NetplanSuite))
}

const (
	twoSubnets = `{"Subnet":"10.0.0.0/24","Gateway":"10.0.0.1"},` +
		`{"Subnet":"10.0.1.0/24","Gateway":"10.0.1.1"}`
	inspectOK = `{"Id":"x","Name":"devsy-pool-a-out","Driver":"bridge","Internal":false,` +
		`"EnableIPv6":false,"Labels":{"devsy.sh/pool-shared":"a"},"Scope":"local",` +
		`"Options":{"com.docker.network.bridge.enable_icc":"false"},` +
		`"IPAM":{"Config":[{"Subnet":"172.20.0.0/16","Gateway":"172.20.0.1"}]}}`
	sessionNetOK = `{"Id":"y","Driver":"bridge","Internal":true,` +
		`"IPAM":{"Config":[{"Subnet":"172.21.0.0/16","Gateway":"172.21.0.1"}]}}`
)

func (s *NetplanSuite) TestAGoodBridgePassesTheCheck() {
	n := s.parse(inspectOK)
	s.NoError(n.checkBridge("a"))
	sub, err := n.subnet()
	s.Require().NoError(err)
	s.Equal(netip.MustParsePrefix("172.20.0.0/16"), sub.prefix)
	s.Equal(netip.MustParseAddr("172.20.0.1"), sub.gateway)
}

const (
	wantICC    = "between containers"
	wantShared = "shared label"
	wantOption = "unexpected option"
	wantPlain  = "not a plain local bridge"
	flagTrue   = "true"
)

type bridgeCase struct {
	mutate func(*netInfo)
	want   string
}

func bridgeCases() map[string]bridgeCase {
	return map[string]bridgeCase{
		"driver":          {func(n *netInfo) { n.Driver = "macvlan" }, "driver"},
		"internal":        {func(n *netInfo) { n.Internal = true }, "internal"},
		"icc true":        {func(n *netInfo) { n.Options[optICC] = flagTrue }, wantICC},
		"icc missing":     {func(n *netInfo) { delete(n.Options, optICC) }, wantICC},
		"icc uppercase":   {func(n *netInfo) { n.Options[optICC] = "False" }, wantICC},
		"no masquerade":   {func(n *netInfo) { n.Options[optMasq] = "false" }, wantOption},
		"masquerade 0":    {func(n *netInfo) { n.Options[optMasq] = "0" }, wantOption},
		"masquerade caps": {func(n *netInfo) { n.Options[optMasq] = "False" }, wantOption},
		"routed gateway": {
			func(n *netInfo) { n.Options["com.docker.network.bridge.gateway_mode_ipv4"] = "routed" },
			wantOption,
		},
		"host bridge name": {
			func(n *netInfo) { n.Options["com.docker.network.bridge.name"] = "docker0" },
			wantOption,
		},
		"host binding": {
			func(n *netInfo) { n.Options["com.docker.network.bridge.host_binding_ipv4"] = "0.0.0.0" },
			wantOption,
		},
		"mtu": {
			func(n *netInfo) { n.Options["com.docker.network.driver.mtu"] = "1500" },
			wantOption,
		},
		"swarm scope": {func(n *netInfo) { n.Scope = "swarm" }, wantPlain},
		"no scope":    {func(n *netInfo) { n.Scope = "" }, wantPlain},
		"attachable":  {func(n *netInfo) { n.Attach = true }, wantPlain},
		"ingress":     {func(n *netInfo) { n.Ingress = true }, wantPlain},
		"config only": {func(n *netInfo) { n.CfgOnly = true }, wantPlain},
		"config from": {func(n *netInfo) { n.CfgFrom.Network = "template" }, wantPlain},
		"wrong name":  {func(n *netInfo) { n.Name = "devsy-pool-a-other" }, "name"},
		"no shared label": {
			func(n *netInfo) { n.Labels = nil }, wantShared,
		},
		"other pool's label": {
			func(n *netInfo) { n.Labels[provider.LabelShared] = "b" }, wantShared,
		},
		"empty label value": {
			func(n *netInfo) { n.Labels[provider.LabelShared] = "" }, wantShared,
		},
		"unrelated label only": {
			func(n *netInfo) { n.Labels = map[string]string{"owner": "someone"} }, wantShared,
		},
		"session pool label": {
			func(n *netInfo) { n.Labels[provider.LabelPool] = "a" }, "session pool label",
		},
		"ipv6":      {func(n *netInfo) { n.IPv6 = true }, "IPv6"},
		"no subnet": {func(n *netInfo) { n.IPAM.Config = nil }, "0 subnets"},
		"two subnets": {func(n *netInfo) {
			n.IPAM.Config = append(n.IPAM.Config, ipamConfig{"10.9.0.0/24", "10.9.0.1"})
		}, "2 subnets"},
		"gateway outside": {func(n *netInfo) { n.IPAM.Config[0].Gateway = "10.9.0.1" }, "gateway"},
	}
}

// Each case starts from a bridge that passes and breaks one field, so a case
// can only fail on the check it names.
func (s *NetplanSuite) TestABridgeThatDiffersInOneCheckedSettingIsRefused() {
	s.Require().NoError(s.parse(inspectOK).checkBridge("a"), "the base bridge must pass")
	for name, tc := range bridgeCases() {
		n := s.parse(inspectOK)
		tc.mutate(&n)
		err := n.checkBridge("a")
		s.Require().Error(err, name)
		s.Contains(err.Error(), tc.want, name)
	}
}

func (s *NetplanSuite) TestAnEmptyPoolNeverMatchesAnUnlabelledNetwork() {
	n := s.parse(inspectOK)
	n.Name = "devsy-pool--out"
	n.Labels = nil
	err := n.checkBridge("")
	s.Require().Error(err)
	s.Contains(err.Error(), "empty")
}

func (s *NetplanSuite) TestABridgeOfAnotherPoolIsRefused() {
	err := s.parse(inspectOK).checkBridge("b")
	s.Require().Error(err)
	s.Contains(err.Error(), "name")
}

func (s *NetplanSuite) TestMasqueradeMayBeExplicitlyOn() {
	n := s.parse(inspectOK)
	n.Options[optMasq] = flagTrue
	s.NoError(n.checkBridge("a"))
}

func (s *NetplanSuite) TestASessionNetworkMustBeInternal() {
	s.NoError(s.parse(sessionNetOK).checkInternal())
	s.Error(s.parse(inspectOK).checkInternal())
}

func (s *NetplanSuite) TestSubnetRefusesWhatTheAddressPlanCannotUse() {
	cases := map[string]string{
		"none":         `{"IPAM":{"Config":[]}}`,
		"two":          `{"IPAM":{"Config":[` + twoSubnets + `]}}`,
		"ipv6 flag":    `{"EnableIPv6":true,"IPAM":{"Config":[{"Subnet":"10.0.0.0/24","Gateway":"10.0.0.1"}]}}`,
		"ipv6 subnet":  `{"IPAM":{"Config":[{"Subnet":"fd00::/64","Gateway":"fd00::1"}]}}`,
		"noncanonical": `{"IPAM":{"Config":[{"Subnet":"10.0.0.5/24","Gateway":"10.0.0.1"}]}}`,
		"no gateway":   `{"IPAM":{"Config":[{"Subnet":"10.0.0.0/24"}]}}`,
		"gateway out":  `{"IPAM":{"Config":[{"Subnet":"10.0.0.0/24","Gateway":"10.0.1.1"}]}}`,
		"bad subnet":   `{"IPAM":{"Config":[{"Subnet":"nope","Gateway":"10.0.0.1"}]}}`,
	}
	for name, doc := range cases {
		_, err := s.parse(doc).subnet()
		s.Error(err, name)
	}
}

func (s *NetplanSuite) TestParseNetworkRefusesBadJSON() {
	_, err := parseNetwork([]byte("{"))
	s.Error(err)
}

func (s *NetplanSuite) TestTheSidecarGetsTheFirstFreeHostAfterTheGateway() {
	sub := subnet{netip.MustParsePrefix("172.21.0.0/16"), netip.MustParseAddr("172.21.0.1")}
	got, err := sidecarAddr(sub, nil)
	s.Require().NoError(err)
	s.Equal(netip.MustParseAddr("172.21.0.2"), got)

	got, err = sidecarAddr(sub, []netip.Addr{netip.MustParseAddr("172.21.0.2")})
	s.Require().NoError(err)
	s.Equal(netip.MustParseAddr("172.21.0.3"), got)
}

func (s *NetplanSuite) TestTheSidecarAddressNeverIsTheBroadcastOrOutsideTheSubnet() {
	sub := subnet{netip.MustParsePrefix("10.0.0.0/30"), netip.MustParseAddr("10.0.0.1")}
	got, err := sidecarAddr(sub, nil)
	s.Require().NoError(err)
	s.Equal(netip.MustParseAddr("10.0.0.2"), got)

	_, err = sidecarAddr(sub, []netip.Addr{got})
	s.Error(err, "10.0.0.3 is the broadcast address")

	last := subnet{netip.MustParsePrefix("10.0.0.0/30"), netip.MustParseAddr("10.0.0.2")}
	_, err = sidecarAddr(last, nil)
	s.Error(err)
}

func (s *NetplanSuite) TestTinySubnetsHaveNoSidecarAddress() {
	for _, in := range [][2]string{
		{"10.0.0.0/31", "10.0.0.0"},
		{"10.0.0.4/32", "10.0.0.4"},
		{"255.255.255.252/30", "255.255.255.253"},
	} {
		sub := subnet{netip.MustParsePrefix(in[0]), netip.MustParseAddr(in[1])}
		got, err := sidecarAddr(sub, nil)
		if in[0] == "255.255.255.252/30" {
			s.Require().NoError(err)
			s.Equal(netip.MustParseAddr("255.255.255.254"), got)
			continue
		}
		s.Error(err, in[0])
	}
}

func (s *NetplanSuite) TestLastAddrIsTheBroadcastAddress() {
	for in, want := range map[string]string{
		"172.21.0.0/16":  "172.21.255.255",
		"10.0.0.0/30":    "10.0.0.3",
		"192.168.1.0/24": "192.168.1.255",
		"10.0.0.4/32":    "10.0.0.4",
		"10.0.0.0/8":     "10.255.255.255",
		"0.0.0.0/0":      "255.255.255.255",
	} {
		s.Equal(netip.MustParseAddr(want), lastAddr(netip.MustParsePrefix(in)), in)
	}
}

func (s *NetplanSuite) TestTheBindAddressUsesTheProxyPort() {
	s.Equal("172.21.0.2:3128", bindAddress(netip.MustParseAddr("172.21.0.2")))
}

func (s *NetplanSuite) TestAlwaysDenyHoldsEverySessionSubnetTheBridgeAndTheGateways() {
	bridge := subnet{netip.MustParsePrefix("172.20.0.0/16"), netip.MustParseAddr("172.20.0.1")}
	nets := []subnet{
		{netip.MustParsePrefix("172.21.0.0/16"), netip.MustParseAddr("172.21.0.1")},
		{netip.MustParsePrefix("172.22.0.0/16"), netip.MustParseAddr("172.22.0.1")},
		{netip.MustParsePrefix("172.21.0.0/16"), netip.MustParseAddr("172.21.0.1")},
	}
	s.Equal([]string{
		"172.20.0.0/16", "172.20.0.1/32",
		"172.21.0.0/16", "172.21.0.1/32",
		"172.22.0.0/16", "172.22.0.1/32",
	}, alwaysDeny(nets, bridge))
	s.Equal([]string{"172.20.0.0/16", "172.20.0.1/32"}, alwaysDeny(nil, bridge))
}

func (s *NetplanSuite) TestACapacityFailureMapsToTheAddressSpaceError() {
	for _, text := range []string{
		"Error response from daemon: all predefined address pools have been fully subnetted",
		"Error response from daemon: could not find an available, non-overlapping IPv4 address " +
			"pool among the defined address pools: [{172.17.0.0 16}]",
	} {
		err := mapCreateError(
			&CommandError{Command: kindNetwork + " " + verbCreate, Stderr: text, Err: errInjected},
		)
		s.ErrorIs(err, ErrNoAddressSpace, text)
		s.ErrorIs(err, errInjected, "the cause stays in the chain")
	}
}

func (s *NetplanSuite) TestAnExplicitSubnetOverlapIsNotACapacityError() {
	overlap := &CommandError{
		Command: kindNetwork + " " + verbCreate,
		Stderr:  "Error response from daemon: Pool overlaps with other one on this address space",
		Err:     errInjected,
	}
	s.NotErrorIs(mapCreateError(overlap), ErrNoAddressSpace)
}

func (s *NetplanSuite) TestASessionNetworkWithIPv6OrNoSubnetIsRefused() {
	s.Error(
		s.parse(`{"Internal":true,"EnableIPv6":true,"IPAM":{"Config":[{"Subnet":"10.0.0.0/24","Gateway":"10.0.0.1"}]}}`).
			checkInternal(),
	)
	s.Error(s.parse(`{"Internal":true}`).checkInternal())
}

func (s *NetplanSuite) TestOtherErrorsAreLeftAlone() {
	other := &CommandError{Command: "network create", Stderr: "boom", Err: errInjected}
	s.NotErrorIs(mapCreateError(other), ErrNoAddressSpace)
	plain := errors.New("plain")
	s.Equal(plain, mapCreateError(plain))
	s.NoError(mapCreateError(nil))
}

func (s *NetplanSuite) parse(doc string) netInfo {
	n, err := parseNetwork([]byte(doc))
	s.Require().NoError(err)
	return n
}
