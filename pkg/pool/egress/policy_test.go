package egress

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/stretchr/testify/suite"
)

const (
	hostGitHub  = "github.com"
	hostWild    = "*.example.com"
	hostPaaS    = "api.paas.example.org"
	hostSub     = "a.example.com"
	loopback6   = "::1"
	zonedLL     = "fe80::1%eth0"
	publicIP    = "140.82.112.3"
	privateIP   = "10.1.2.3"
	metadataIP  = "169.254.169.254"
	cidrPrivate = "10.0.0.0/8"
)

type PolicySuite struct {
	suite.Suite
}

func TestPolicySuite(t *testing.T) {
	suite.Run(t, new(PolicySuite))
}

func addrs(values ...string) []netip.Addr {
	out := make([]netip.Addr, len(values))
	for i, v := range values {
		out[i] = netip.MustParseAddr(v)
	}
	return out
}

func prefixes(values ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(values))
	for i, v := range values {
		out[i] = netip.MustParsePrefix(v)
	}
	return out
}

func newPolicy(tb testing.TB, rules []poolconfig.AllowRule, deny ...string) *Policy {
	tb.Helper()
	p, err := New(
		poolconfig.Egress{Default: poolconfig.EgressDeny, Allow: rules},
		prefixes(deny...),
	)
	if err != nil {
		tb.Fatal(err)
	}
	return p
}

func host(h string, ports ...int) poolconfig.AllowRule {
	return poolconfig.AllowRule{Host: h, Ports: ports}
}

func cidr(c string, ports ...int) poolconfig.AllowRule {
	return poolconfig.AllowRule{CIDR: c, Ports: ports}
}

func (s *PolicySuite) TestReasonsAreTheAuditEnum() {
	for reason, want := range map[Reason]string{
		NotAllowed: "not_allowed", ReservedRange: "reserved_range", PrivateRange: "private_range",
		PoolNetwork: "pool_network", IPLiteral: "ip_literal", ResolveFailed: "resolve_failed",
		BadRequest: "bad_request",
	} {
		s.Equal(want, string(reason))
	}
}

func (s *PolicySuite) TestNewRefusesMalformedRules() {
	bad := []poolconfig.Egress{
		{Default: "allow"},
		{Allow: []poolconfig.AllowRule{{}}},
		{Allow: []poolconfig.AllowRule{{Host: hostGitHub, CIDR: cidrPrivate}}},
		{Allow: []poolconfig.AllowRule{host("Github.com")}},
		{Allow: []poolconfig.AllowRule{host("a.*.com")}},
		{Allow: []poolconfig.AllowRule{host("*")}},
		{Allow: []poolconfig.AllowRule{host("1.2.3.4")}},
		{Allow: []poolconfig.AllowRule{host("localhost")}},
		{Allow: []poolconfig.AllowRule{host(hostGitHub, 0)}},
		{Allow: []poolconfig.AllowRule{host(hostGitHub, 65536)}},
		{Allow: []poolconfig.AllowRule{host(hostGitHub, -1)}},
		{Allow: []poolconfig.AllowRule{cidr("10.0.0.0")}},
		{Allow: []poolconfig.AllowRule{cidr("not-a-cidr")}},
		{Allow: []poolconfig.AllowRule{cidr("::ffff:10.0.0.0/104")}},
		{Allow: []poolconfig.AllowRule{cidr("0.0.0.0/0")}},
		{Allow: []poolconfig.AllowRule{cidr("::/0")}},
		{Allow: []poolconfig.AllowRule{cidr("8.0.0.0/7")}},
		{Allow: []poolconfig.AllowRule{cidr("2000::/16")}},
		{Allow: []poolconfig.AllowRule{cidr("2000::/31")}},
	}
	for i, cfg := range bad {
		_, err := New(cfg, nil)
		s.Error(err, "case %d", i)
	}
	for _, deny := range [][]netip.Prefix{{{}}, prefixes("::ffff:10.0.0.0/104")} {
		_, err := New(poolconfig.Egress{}, deny)
		s.Error(err)
	}
	_, err := New(poolconfig.Egress{}, nil)
	s.NoError(err)
}

func (s *PolicySuite) TestNewKeepsACidrThatOverlapsAReservedRange() {
	_, err := New(poolconfig.Egress{Allow: []poolconfig.AllowRule{cidr("169.254.0.0/16")}}, nil)
	s.NoError(err)
}

func (s *PolicySuite) TestWideCidrErrorNamesTheLimits() {
	for _, c := range []string{"0.0.0.0/0", "8.0.0.0/7", "2000::/31"} {
		_, err := New(poolconfig.Egress{Allow: []poolconfig.AllowRule{cidr(c)}}, nil)
		s.Require().Error(err, c)
		s.Contains(err.Error(), "/8 for IPv4")
		s.Contains(err.Error(), "/32 for IPv6")
	}
}

func (s *PolicySuite) TestAZonedMappedAddressIsRefused() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostSub), cidr(cidrPrivate)})
	for _, a := range []string{"::ffff:10.0.0.1%eth0", "::ffff:8.8.8.8%eth0"} {
		addr := netip.MustParseAddr(a)
		s.Equal(deny(ReservedRange), p.CheckAddr(addr, 443), a)
		s.Equal(deny(ReservedRange), p.CheckResolved(hostSub, 443, []netip.Addr{addr}), a)
	}
	s.Equal(allowed, p.CheckAddr(netip.MustParseAddr("::ffff:10.0.0.1"), 443))
}

func (s *PolicySuite) TestAlwaysDenyIsCopiedByNew() {
	deny := prefixes("172.17.0.0/16")
	p, err := New(poolconfig.Egress{Allow: []poolconfig.AllowRule{host(hostSub)}}, deny)
	s.Require().NoError(err)
	deny[0] = netip.MustParsePrefix(publicIP + "/32")
	s.Equal(PoolNetwork, p.CheckResolved(hostSub, 443, addrs("172.17.0.5")).Reason)
	s.Equal(allowed, p.CheckResolved(hostSub, 443, addrs(publicIP)))
}

func (s *PolicySuite) TestNewKeepsTheNarrowestWideCidr() {
	for _, c := range []string{"8.0.0.0/8", "2000::/32"} {
		_, err := New(poolconfig.Egress{Allow: []poolconfig.AllowRule{cidr(c)}}, nil)
		s.NoError(err, c)
	}
}

func (s *PolicySuite) TestEmptyPolicyDeniesEverything() {
	p := newPolicy(s.T(), nil)
	s.Equal(deny(NotAllowed), p.CheckName(hostGitHub, 443))
	s.Equal(deny(IPLiteral), p.CheckAddr(netip.MustParseAddr(publicIP), 443))
	s.Equal(deny(NotAllowed), p.CheckResolved(hostGitHub, 443, addrs(publicIP)))
}

func (s *PolicySuite) TestHostMatching() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostGitHub), host(hostWild, 443, 8443)})
	cases := []struct {
		host string
		port int
		want Decision
	}{
		{hostGitHub, 443, allowed},
		{"GITHUB.COM", 443, allowed},
		{"github.com.", 443, allowed},
		{"github.com..", 443, deny(BadRequest)},
		{"gist.github.com", 443, deny(NotAllowed)},
		{"evil-github.com", 443, deny(NotAllowed)},
		{"github.com.evil.com", 443, deny(NotAllowed)},
		{hostSub, 443, allowed},
		{subdomainHost, 8443, allowed},
		{"A.Example.COM.", 443, allowed},
		{"example.com", 443, deny(NotAllowed)},
		{"evil-example.com", 443, deny(NotAllowed)},
		{hostSub, 80, deny(NotAllowed)},
		{hostGitHub, 8443, deny(NotAllowed)},
		{"a..example.com", 443, deny(BadRequest)},
		{".example.com", 443, deny(BadRequest)},
		{"", 443, deny(BadRequest)},
		{"g\u0130thub.com", 443, deny(BadRequest)},
		{"github.com\x00.evil.com", 443, deny(BadRequest)},
		{"github.com\n", 443, deny(BadRequest)},
		{"github.com:443", 443, deny(BadRequest)},
		{"user@github.com", 443, deny(BadRequest)},
		{"x.internal", 443, deny(BadRequest)},
		{"xn--e1afmkfd.example.com", 443, allowed},
		{hostGitHub, 0, deny(BadRequest)},
		{hostGitHub, 65536, deny(BadRequest)},
		{hostGitHub, -443, deny(BadRequest)},
	}
	for _, c := range cases {
		s.Equal(c.want, p.CheckName(c.host, c.port), "%q:%d", c.host, c.port)
	}
}

func (s *PolicySuite) TestPortBounds() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostGitHub, 1, 65535)})
	s.Equal(allowed, p.CheckName(hostGitHub, 1))
	s.Equal(allowed, p.CheckName(hostGitHub, 65535))
	s.Equal(deny(BadRequest), p.CheckName(hostGitHub, 0))
	s.Equal(deny(BadRequest), p.CheckName(hostGitHub, 65536))
	c := newPolicy(s.T(), []poolconfig.AllowRule{cidr("8.8.8.0/24", 1, 65535)})
	s.Equal(allowed, c.CheckAddr(netip.MustParseAddr(publicAddr), 65535))
	s.Equal(deny(BadRequest), c.CheckAddr(netip.MustParseAddr(publicAddr), 65536))
}

func (s *PolicySuite) TestAnAddressIsNeverAName() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostWild), cidr(cidrPrivate)})
	for _, h := range []string{
		publicIP, privateIP, loopback6, "[::1]", zonedLL, "::ffff:10.0.0.1", "127.1",
		"0x7f.0.0.1", "2130706433", "1.2.3.4.example.com.1",
	} {
		d := p.CheckName(h, 443)
		s.False(d.Allow, h)
	}
	s.Equal(deny(IPLiteral), p.CheckName(privateIP, 443))
}

func (s *PolicySuite) TestAllowsAPublicAnswerThroughAHostRule() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostGitHub)})
	s.Equal(allowed, p.CheckResolved(hostGitHub, 443, addrs(publicIP, "2606:4700::1111")))
}

func (s *PolicySuite) TestOneForbiddenAnswerDeniesTheRequest() {
	p := newPolicy(
		s.T(),
		[]poolconfig.AllowRule{host(hostWild), cidr(cidrPrivate)},
		"172.17.0.0/16",
	)
	cases := []struct {
		answers []string
		want    Reason
	}{
		{[]string{publicIP, metadataIP}, ReservedRange},
		{[]string{metadataIP, publicIP}, ReservedRange},
		{[]string{publicIP, loopback4}, ReservedRange},
		{[]string{publicIP, loopback6}, ReservedRange},
		{[]string{publicIP, "::ffff:127.0.0.1"}, ReservedRange},
		{[]string{publicIP, "::ffff:169.254.169.254"}, ReservedRange},
		{[]string{publicIP, "64:ff9b::808:808"}, ReservedRange},
		{[]string{publicIP, "2002:808:808::1"}, ReservedRange},
		{[]string{publicIP, "2001:0:4136:e378::1"}, ReservedRange},
		{[]string{publicIP, "::808:808"}, ReservedRange},
		{[]string{publicIP, zonedLL}, ReservedRange},
		{[]string{publicIP, "172.17.0.2"}, PoolNetwork},
		{[]string{publicIP, "192.168.1.1"}, PrivateRange},
		{[]string{publicIP, "fd12::1"}, PrivateRange},
		{[]string{publicIP, "100.64.0.1"}, PrivateRange},
	}
	for _, c := range cases {
		d := p.CheckResolved(hostSub, 443, addrs(c.answers...))
		s.Equal(deny(c.want), d, "%v", c.answers)
	}
}

func (s *PolicySuite) TestAPrivateAnswerNeedsACoveringCidrAndPort() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostWild, 443, 8080), cidr(cidrPrivate, 443)})
	s.Equal(allowed, p.CheckResolved(hostSub, 443, addrs(privateIP, publicIP)))
	s.Equal(deny(PrivateRange), p.CheckResolved(hostSub, 8080, addrs(privateIP)))
	s.Equal(allowed, p.CheckResolved(hostSub, 8080, addrs(publicIP)))
	s.Equal(deny(PrivateRange), p.CheckResolved(hostSub, 443, addrs("192.168.1.1")))
	s.Equal(allowed, p.CheckResolved(hostSub, 443, addrs("::ffff:10.1.2.3")))
}

func (s *PolicySuite) TestACidrAloneNeverAllowsAName() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{cidr(cidrPrivate), cidr("140.82.0.0/16")})
	s.Equal(deny(NotAllowed), p.CheckName(hostSub, 443))
	s.Equal(deny(NotAllowed), p.CheckResolved(hostSub, 443, addrs(privateIP)))
	s.Equal(deny(NotAllowed), p.CheckResolved(hostGitHub, 443, addrs(publicIP)))
}

func (s *PolicySuite) TestNoRuleReachesAReservedRange() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{
		host(hostWild), cidr("169.254.0.0/16"), cidr("127.0.0.0/8"), cidr("::/32"),
		cidr("0.0.0.0/8"), cidr("64:ff9b::/96"), cidr("fe80::/32"), cidr("100.100.100.200/32"),
	})
	for _, a := range []string{
		metadataIP, loopback4, loopback6, "64:ff9b::808:808", "fe80::1", "100.100.100.200",
		"::ffff:169.254.169.254", "0.0.0.0", "224.0.0.1", "255.255.255.255",
	} {
		s.Equal(deny(ReservedRange), p.CheckResolved(hostSub, 443, addrs(a)), a)
		s.Equal(deny(ReservedRange), p.CheckAddr(netip.MustParseAddr(a), 443), a)
	}
}

func (s *PolicySuite) TestAlwaysDenyBeatsEveryRule() {
	p := newPolicy(s.T(),
		[]poolconfig.AllowRule{host(hostWild), cidr("172.16.0.0/12"), cidr(publicIP + "/32")},
		"172.17.0.0/16", publicIP+"/32", "fd00:1::/32",
	)
	s.Equal(deny(PoolNetwork), p.CheckResolved(hostSub, 443, addrs("172.17.0.5")))
	s.Equal(deny(PoolNetwork), p.CheckAddr(netip.MustParseAddr("172.17.0.5"), 443))
	s.Equal(deny(PoolNetwork), p.CheckResolved(hostSub, 443, addrs(publicIP)))
	s.Equal(deny(PoolNetwork), p.CheckAddr(netip.MustParseAddr(publicIP), 443))
	s.Equal(deny(PoolNetwork), p.CheckAddr(netip.MustParseAddr("::ffff:172.17.0.5"), 443))
	s.Equal(allowed, p.CheckAddr(netip.MustParseAddr("172.18.0.5"), 443))
}

func (s *PolicySuite) TestALiteralNeedsACidrAndAPort() {
	p := newPolicy(
		s.T(),
		[]poolconfig.AllowRule{host(hostGitHub), cidr("140.82.112.0/24"), cidr(cidrPrivate, 8080)},
	)
	cases := []struct {
		addr string
		port int
		want Decision
	}{
		{publicIP, 443, allowed},
		{publicIP, 80, deny(IPLiteral)},
		{"140.82.113.1", 443, deny(IPLiteral)},
		{publicAddr, 443, deny(IPLiteral)},
		{privateIP, 8080, allowed},
		{privateIP, 443, deny(IPLiteral)},
		{"::ffff:10.1.2.3", 8080, allowed},
		{"::ffff:140.82.112.3", 443, allowed},
		{publicIP, 0, deny(BadRequest)},
		{publicIP, 70000, deny(BadRequest)},
		{zonedLL, 443, deny(ReservedRange)},
	}
	for _, c := range cases {
		s.Equal(c.want, p.CheckAddr(netip.MustParseAddr(c.addr), c.port), "%s:%d", c.addr, c.port)
	}
	s.Equal(deny(ReservedRange), p.CheckAddr(netip.Addr{}, 443))
}

func (s *PolicySuite) TestAHostRuleNeverAllowsALiteral() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostGitHub)})
	s.Equal(deny(IPLiteral), p.CheckAddr(netip.MustParseAddr(publicIP), 443))
}

func (s *PolicySuite) TestDeniedNameNeedsNoLookup() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostGitHub)})
	s.Equal(deny(NotAllowed), p.CheckName(hostPaaS, 443))
	s.Equal(deny(NotAllowed), p.CheckResolved(hostPaaS, 443, addrs(publicIP)))
	s.Equal(deny(BadRequest), p.CheckResolved("a..b", 443, addrs(publicIP)))
}

func (s *PolicySuite) TestAnswerCount() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostGitHub)})
	s.Equal(deny(ResolveFailed), p.CheckResolved(hostGitHub, 443, nil))
	s.Equal(deny(ResolveFailed), p.CheckResolved(hostGitHub, 443, []netip.Addr{}))
	s.Equal(deny(ReservedRange), p.CheckResolved(hostGitHub, 443, []netip.Addr{{}}))
	many := make([]netip.Addr, maxAnswers)
	for i := range many {
		many[i] = netip.MustParseAddr(publicIP)
	}
	s.Equal(allowed, p.CheckResolved(hostGitHub, 443, many))
	s.Equal(deny(ResolveFailed), p.CheckResolved(hostGitHub, 443, append(many, many[0])))
}

func (s *PolicySuite) TestDefaultPortIs443() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostGitHub), cidr(cidrPrivate)})
	s.Equal(allowed, p.CheckName(hostGitHub, 443))
	s.Equal(deny(NotAllowed), p.CheckName(hostGitHub, 80))
	s.Equal(allowed, p.CheckAddr(netip.MustParseAddr(privateIP), 443))
}

func (s *PolicySuite) TestRulesAreCopied() {
	ports := []int{443}
	rules := []poolconfig.AllowRule{host(hostGitHub, ports...)}
	p := newPolicy(s.T(), rules)
	ports[0] = 80
	s.Equal(allowed, p.CheckName(hostGitHub, 443))
	s.Equal(deny(NotAllowed), p.CheckName(hostGitHub, 80))
}

func (s *PolicySuite) TestLongNamesAreRefused() {
	p := newPolicy(s.T(), []poolconfig.AllowRule{host(hostWild)})
	long := strings.Repeat("a", 64) + ".example.com"
	s.Equal(deny(BadRequest), p.CheckName(long, 443))
	s.Equal(deny(BadRequest), p.CheckName(strings.Repeat("a.", 130)+"example.com", 443))
}
