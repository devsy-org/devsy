package docker

import (
	"errors"
	"net/netip"
	"testing"

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
	inspectOK = `{"Id":"x","Driver":"bridge","Internal":false,"EnableIPv6":false,` +
		`"Options":{"com.docker.network.bridge.enable_icc":"false"},` +
		`"IPAM":{"Config":[{"Subnet":"172.20.0.0/16","Gateway":"172.20.0.1"}]}}`
	sessionNetOK = `{"Id":"y","Driver":"bridge","Internal":true,` +
		`"IPAM":{"Config":[{"Subnet":"172.21.0.0/16","Gateway":"172.21.0.1"}]}}`
)

func (s *NetplanSuite) TestAGoodBridgePassesTheCheck() {
	n := s.parse(inspectOK)
	s.NoError(n.checkBridge())
	sub, err := n.subnet()
	s.Require().NoError(err)
	s.Equal(netip.MustParsePrefix("172.20.0.0/16"), sub.prefix)
	s.Equal(netip.MustParseAddr("172.20.0.1"), sub.gateway)
}

func (s *NetplanSuite) TestABridgeThatDiffersInAnyCheckedSettingIsRefused() {
	cases := map[string]string{
		"driver":        `{"Driver":"macvlan","Options":{"com.docker.network.bridge.enable_icc":"false"}}`,
		"internal":      `{"Driver":"bridge","Internal":true,"Options":{"com.docker.network.bridge.enable_icc":"false"}}`,
		"icc true":      `{"Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"true"}}`,
		"icc missing":   `{"Driver":"bridge"}`,
		"icc uppercase": `{"Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"False"}}`,
		"empty":         `{}`,
	}
	for name, doc := range cases {
		s.Error(s.parse(doc).checkBridge(), name)
	}
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

func (s *NetplanSuite) TestLastAddrIsTheBroadcastAddress() {
	for in, want := range map[string]string{
		"172.21.0.0/16":  "172.21.255.255",
		"10.0.0.0/30":    "10.0.0.3",
		"192.168.1.0/24": "192.168.1.255",
		"10.0.0.4/32":    "10.0.0.4",
		"10.0.0.0/8":     "10.255.255.255",
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
		"Error response from daemon: Pool overlaps with other one on this address space",
	} {
		err := mapCreateError(
			&CommandError{Command: kindNetwork + " " + verbCreate, Stderr: text, Err: errInjected},
		)
		s.ErrorIs(err, ErrNoAddressSpace, text)
		s.ErrorIs(err, errInjected, "the cause stays in the chain")
	}
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
