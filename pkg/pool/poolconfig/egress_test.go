package poolconfig

const (
	wantNarrower = "narrower"
	wantOutside  = "outside"
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
	{"numeric tld", func(p *Pool) { allow(p, AllowRule{Host: "127.1"}) }, "top-level domain"},
	{"hex host", func(p *Pool) { allow(p, AllowRule{Host: "0x7f.1"}) }, "top-level domain"},
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
	{
		"nat64 metadata",
		func(p *Pool) { allow(p, AllowRule{CIDR: "64:ff9b::a9fe:a9fe/128"}) },
		"64:ff9b::/96",
	},
	{"6to4", func(p *Pool) { allow(p, AllowRule{CIDR: "2002:a9fe:a9fe::/48"}) }, "2002::/16"},
	{"unspecified ipv6", func(p *Pool) { allow(p, AllowRule{CIDR: "::/128"}) }, "::/128"},
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
}

func (s *PoolConfigSuite) TestEgressRequiresExactFieldNames() {
	_, err := Parse(append(s.readMinimal(), []byte("  egress: {allow: [{Host: a.com}]}\n")...))
	s.ErrorContains(err, `spec.egress.allow: unknown field "Host"`)
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
}

func allow(p *Pool, rule AllowRule) {
	p.Spec.Egress.Allow = append(p.Spec.Egress.Allow, rule)
}
