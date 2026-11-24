package poolconfig

import "strings"

const (
	wantNarrower = "narrower"
	wantOutside  = "outside"
	wantTLD      = "top-level domain"
)

var egressRejections = []rejection{
	{"default allow", func(p *Pool) { p.Spec.Egress.Default = "allow" }, "egress.default"},
	{
		"host and cidr",
		func(p *Pool) { allow(p, AllowRule{Host: "x.com", CIDR: "10.0.0.0/8"}) },
		"exactly one",
	},
	{"empty rule", func(p *Pool) { allow(p, AllowRule{}) }, "exactly one"},
	{"bare wildcard", func(p *Pool) { allow(p, AllowRule{Host: "*"}) }, "wildcards"},
	{"tld wildcard", func(p *Pool) { allow(p, AllowRule{Host: "*.com"}) }, "fully qualified"},
	{"inner wildcard", func(p *Pool) { allow(p, AllowRule{Host: "*.*.a.com"}) }, "wildcards"},
	{"ip host", func(p *Pool) { allow(p, AllowRule{Host: "1.2.3.4"}) }, "use cidr"},
	{"single label", func(p *Pool) { allow(p, AllowRule{Host: "intranet"}) }, "fully qualified"},
	{"uppercase host", func(p *Pool) { allow(p, AllowRule{Host: "A.com"}) }, "lowercase"},
	{"bad label", func(p *Pool) { allow(p, AllowRule{Host: "a_b.com"}) }, "valid DNS"},
	{"numeric tld", func(p *Pool) { allow(p, AllowRule{Host: "127.1"}) }, wantTLD},
	{"hex host", func(p *Pool) { allow(p, AllowRule{Host: "0x7f.1"}) }, wantTLD},
	{"hex tld", func(p *Pool) { allow(p, AllowRule{Host: "0x7f.0x1"}) }, wantTLD},
	{"localhost suffix", func(p *Pool) { allow(p, AllowRule{Host: "*.a.localhost"}) }, "localhost"},
	{
		"port zero",
		func(p *Pool) { allow(p, AllowRule{Host: "y.com", Ports: []int{0}}) },
		wantOutside,
	},
	{
		"port high",
		func(p *Pool) { allow(p, AllowRule{Host: "z.com", Ports: []int{70000}}) },
		wantOutside,
	},
	{"bad cidr", func(p *Pool) { allow(p, AllowRule{CIDR: "10.0.0.0/33"}) }, "valid CIDR"},
	{"all ipv4", func(p *Pool) { allow(p, AllowRule{CIDR: "0.0.0.0/0"}) }, wantNarrower},
	{"all ipv6", func(p *Pool) { allow(p, AllowRule{CIDR: "::/0"}) }, wantNarrower},
	{"broad ipv4", func(p *Pool) { allow(p, AllowRule{CIDR: "192.0.0.0/2"}) }, wantNarrower},
	{"broad ipv6", func(p *Pool) { allow(p, AllowRule{CIDR: "2000::/3"}) }, wantNarrower},
	{"metadata", func(p *Pool) { allow(p, AllowRule{CIDR: "169.254.169.254/32"}) }, "metadata"},
	{"covers loopback", func(p *Pool) { allow(p, AllowRule{CIDR: "127.0.0.0/16"}) }, "127.0.0.0/8"},
	{"ipv6 link local", func(p *Pool) { allow(p, AllowRule{CIDR: "fe80::/64"}) }, "fe80::/10"},
	{
		"ipv6 metadata",
		func(p *Pool) { allow(p, AllowRule{CIDR: "fd00:ec2::254/128"}) },
		"fd00:ec2::/32",
	},
	{
		"mapped loopback",
		func(p *Pool) { allow(p, AllowRule{CIDR: "::ffff:127.0.0.1/128"}) },
		"::ffff:0.0.0.0/96",
	},
	{
		"mapped metadata",
		func(p *Pool) { allow(p, AllowRule{CIDR: "::ffff:169.254.169.254/128"}) },
		"::ffff:0.0.0.0/96",
	},
	{"unspecified", func(p *Pool) { allow(p, AllowRule{CIDR: "0.0.0.0/32"}) }, "0.0.0.0/8"},
	{
		"oracle metadata",
		func(p *Pool) { allow(p, AllowRule{CIDR: "192.0.0.192/32"}) },
		"192.0.0.192/32",
	},
	{
		"local nat64",
		func(p *Pool) { allow(p, AllowRule{CIDR: "64:ff9b:1::/64"}) },
		"64:ff9b:1::/48",
	},
	{
		"zone id",
		func(p *Pool) { allow(p, AllowRule{CIDR: "2606:4700::1%eth0/128"}) },
		"valid CIDR",
	},
	{
		"translated metadata",
		func(p *Pool) { allow(p, AllowRule{CIDR: "::ffff:0:a9fe:a9fe/128"}) },
		"::ffff:0:0:0/96",
	},
	{
		"nat64 metadata",
		func(p *Pool) { allow(p, AllowRule{CIDR: "64:ff9b::a9fe:a9fe/128"}) },
		"64:ff9b::/96",
	},
	{"6to4", func(p *Pool) { allow(p, AllowRule{CIDR: "2002:a9fe:a9fe::/48"}) }, "2002::/16"},
	{"unspecified ipv6", func(p *Pool) { allow(p, AllowRule{CIDR: "::/128"}) }, "::/96"},
	{
		"ipv4 compatible",
		func(p *Pool) { allow(p, AllowRule{CIDR: "::192.0.2.1/128"}) },
		"::/96",
	},
	{
		"teredo",
		func(p *Pool) { allow(p, AllowRule{CIDR: "2001:0:a9fe:a9fe::/64"}) },
		"2001::/32",
	},
	{"ipv4 multicast", func(p *Pool) { allow(p, AllowRule{CIDR: "224.0.0.0/8"}) }, "224.0.0.0/4"},
	{"ipv6 multicast", func(p *Pool) { allow(p, AllowRule{CIDR: "ff02::/32"}) }, "ff00::/8"},
	{"broadcast", func(p *Pool) { allow(p, AllowRule{CIDR: "255.255.255.255/32"}) }, "240.0.0.0/4"},
	{
		"non canonical cidr",
		func(p *Pool) { allow(p, AllowRule{CIDR: "10.1.2.3/8"}) },
		"must be canonical",
	},
	{
		"azure wireserver",
		func(p *Pool) { allow(p, AllowRule{CIDR: "168.63.129.0/24"}) },
		"168.63.129.16",
	},
	{
		"alibaba metadata",
		func(p *Pool) { allow(p, AllowRule{CIDR: "100.100.100.200/32"}) },
		"100.100.100.200",
	},
}

func (s *PoolConfigSuite) TestEgressRejections() {
	s.runRejections(egressRejections)
}

func (s *PoolConfigSuite) TestEgressDefaults() {
	s.Equal(EgressDeny, s.parseMinimal("").Spec.Egress.Default)
	p := s.loadExample()
	s.Equal([]int{443}, p.Spec.Egress.Allow[0].Ports)
	s.Equal([]int{443, 22}, p.Spec.Egress.Allow[2].Ports)
	s.Equal(
		s.version(string(s.readMinimal())),
		s.version(string(s.readMinimal())+"  egress: {default: deny}\n"),
	)

	withPorts := func(ports string) string {
		return s.version(string(s.readMinimal()) +
			"  egress:\n    allow:\n      - {host: a.com" + ports + "}\n")
	}
	s.Equal(withPorts(""), withPorts(", ports: [443]"))
	s.NotEqual(withPorts(""), withPorts(", ports: [8443]"))
}

func (s *PoolConfigSuite) TestEgressRequiresExactFieldNames() {
	_, err := Parse(append(s.readMinimal(), []byte("  egress: {allow: [{Host: a.com}]}\n")...))
	s.EqualError(err, "parse pool config: spec.egress.allow[0] (line 13): unknown field")
}

func (s *PoolConfigSuite) TestHostMatches() {
	s.True(HostMatches("github.com", "github.com"))
	s.True(HostMatches("github.com", "GitHub.com."))
	s.False(HostMatches("github.com", "api.github.com"))
	s.True(HostMatches("*.npmjs.org", "registry.npmjs.org"))
	s.True(HostMatches("*.npmjs.org", "a.b.npmjs.org"))
	s.False(HostMatches("*.npmjs.org", "npmjs.org"))
	s.False(HostMatches("*.npmjs.org", "evilnpmjs.org"))
	s.False(HostMatches("*.example.com", ".example.com"))
	s.False(HostMatches("*.example.com", "*.example.com"))
	s.False(HostMatches("*.example.com", "a b.example.com"))
	s.False(HostMatches("*.com", "example.com"))
	s.False(HostMatches("GitHub.com", "github.com"))
	s.False(HostMatches("github.com", "g\u0130thub.com"))
}

func (s *PoolConfigSuite) TestEgressErrorsDoNotEchoValues() {
	p := s.loadExample()
	allow(p, AllowRule{Host: "a.com", Ports: []int{987654321}})
	allow(p, AllowRule{CIDR: "10.1.2.3/16"})
	err := p.Validate()
	s.ErrorContains(err, "ports[0]: is outside")
	s.ErrorContains(err, "must be canonical")
	for _, v := range []string{"987654321", "10.1.2.3", "10.1.0.0"} {
		s.NotContains(err.Error(), v)
	}
}

func (s *PoolConfigSuite) TestEgressDecodeErrorsDoNotEchoValues() {
	extra := "  egress: {allow: [{host: a.com, ports: [!!int CANARY]}]}\n"
	_, err := Parse(append(s.readMinimal(), []byte(extra)...))
	s.ErrorContains(err, "spec.egress.allow[0].ports[0] (line 13): must be a whole number")
	s.NotContains(err.Error(), "CANARY")
}

func (s *PoolConfigSuite) TestEgressParseErrorsGiveLines() {
	for _, tc := range []struct{ extra, want string }{
		{"  egress: {allow: !!null [{host: a.com}]}\n", "spec.egress.allow (line 13): must be a list"},
		{"  egress: {allow: [{host: a.com, ports: !!null [22]}]}\n", "ports (line 13): must be a list"},
		{"  egress: {allow: [{host: a.com, ports: [70000]}]}\n", "ports[0] (line 13): is outside"},
	} {
		_, err := Parse(append(s.readMinimal(), []byte(tc.extra)...))
		s.ErrorContains(err, tc.want)
	}
}

func (s *PoolConfigSuite) TestInternalNamesRefused() {
	for _, host := range []string{
		"metadata.google.internal", "*.ec2.internal", "kubernetes.default.svc", "*.svc.cluster.local",
		"printer.local", "a.localdomain", "router.home.arpa", "metadata.goog",
	} {
		p := s.loadExample()
		allow(p, AllowRule{Host: host})
		p.Spec.Credentials[1].Route.Upstream = "https://" + strings.TrimPrefix(host, "*.")
		err := p.Validate()
		s.Require().ErrorContains(err, "host: must not be an internal or metadata name", host)
		s.ErrorContains(err, "route.upstream: host must not be an internal or metadata name", host)
		s.False(HostMatches("*.example.com", strings.TrimPrefix(host, "*.")), host)
	}
	for _, host := range []string{"internal.example.com", "local.example.com", "svc.example.com"} {
		p := s.loadExample()
		allow(p, AllowRule{Host: host})
		s.NoError(p.Validate(), host)
	}
}

func allow(p *Pool, rule AllowRule) {
	p.Spec.Egress.Allow = append(p.Spec.Egress.Allow, rule)
}
