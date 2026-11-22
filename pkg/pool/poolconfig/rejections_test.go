package poolconfig

import "time"

type rejection struct {
	name   string
	mutate func(p *Pool)
	want   string
}

const (
	nameRule     = "must be 1 to 32"
	wantNarrower = "narrower"
	wantHeader   = "route.header"
	wantOutside  = "outside"
)

// rejections starts from testdata/pool.yaml, breaks one rule each and expects
// the error to mention want.
var rejections = []rejection{
	{
		"upstream port",
		func(p *Pool) { p.Spec.Credentials[1].Route.Upstream = "https://x.com:99999" },
		wantOutside,
	},
	{"name uppercase", func(p *Pool) { p.Metadata.Name = "Demo" }, nameRule},
	{
		"name too long",
		func(p *Pool) { p.Metadata.Name = "a23456789012345678901234567890123" },
		nameRule,
	},
	{
		"absolute devcontainer",
		func(p *Pool) { p.Spec.Source.DevcontainerPath = "/etc/dc.json" },
		"devcontainerPath",
	},
	{
		"escaping devcontainer",
		func(p *Pool) { p.Spec.Source.DevcontainerPath = "../x.json" },
		"devcontainerPath",
	},
	{"no concurrency", func(p *Pool) { p.Spec.Capacity.MaxConcurrent = -1 }, "maxConcurrent"},
	{"negative queue", func(p *Pool) { p.Spec.Capacity.MaxQueued = -1 }, "maxQueued"},
	{"too many warm", func(p *Pool) { p.Spec.Capacity.Warm = 5 }, "capacity.warm"},
	{
		"idle over ttl",
		func(p *Pool) { p.Spec.Lifecycle.IdleTimeout = dur(9 * time.Hour) },
		"idleTimeout",
	},
	{
		"negative job timeout",
		func(p *Pool) { p.Spec.Lifecycle.JobTimeout = dur(-time.Minute) },
		"jobTimeout",
	},
	{"negative ttl", func(p *Pool) { p.Spec.Lifecycle.TTL = dur(-time.Minute) }, "lifecycle.ttl"},
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
	{"broad ipv4", func(p *Pool) { allow(p, AllowRule{CIDR: "192.0.0.0/2"}) }, wantNarrower},
	{"broad ipv6", func(p *Pool) { allow(p, AllowRule{CIDR: "2000::/3"}) }, wantNarrower},
	{"numeric tld", func(p *Pool) { allow(p, AllowRule{Host: "127.1"}) }, "top-level domain"},
	{"hex host", func(p *Pool) { allow(p, AllowRule{Host: "0x7f.1"}) }, "top-level domain"},
	{"localhost suffix", func(p *Pool) { allow(p, AllowRule{Host: "*.a.localhost"}) }, "localhost"},
	{
		"hop-by-hop header",
		func(p *Pool) { p.Spec.Credentials[1].Route.Header = "Connection" },
		wantHeader,
	},
	{
		"prefix crlf",
		func(p *Pool) { p.Spec.Credentials[2].Route.Prefix = "Bearer \r\nX-Evil: 1" },
		"printable",
	},
	{
		"duplicate credential",
		func(p *Pool) { p.Spec.Credentials[1].Name = "github" },
		"duplicate credential",
	},
	{"unknown mode", func(p *Pool) { p.Spec.Credentials[0].Mode = "static" }, "mode"},
	{"wrong issuer", func(p *Pool) { p.Spec.Credentials[0].Issuer = "gitlab" }, "issuer"},
	{"bad app id", func(p *Pool) { p.Spec.Credentials[0].AppID = "12a" }, "numeric"},
	{
		"bad private key ref",
		func(p *Pool) { p.Spec.Credentials[0].PrivateKey = "sops:/x" },
		"privateKey",
	},
	{"no scope", func(p *Pool) { p.Spec.Credentials[0].Scope = nil }, "scope: is required"},
	{
		"bad repo",
		func(p *Pool) { p.Spec.Credentials[0].Scope.Repositories = []string{"api"} },
		"owner/repo",
	},
	{
		"no permissions",
		func(p *Pool) { p.Spec.Credentials[0].Scope.Permissions = nil },
		"permission",
	},
	{
		"admin permission",
		func(p *Pool) { p.Spec.Credentials[0].Scope.Permissions["contents"] = "admin" },
		"read or write",
	},
	{
		"mint ttl too long",
		func(p *Pool) { p.Spec.Credentials[0].TTL = dur(2 * time.Hour) },
		"at most 1h",
	},
	{
		"unknown delivery",
		func(p *Pool) { p.Spec.Credentials[0].Deliver = []string{"file"} },
		"unknown target",
	},
	{
		"mint with route",
		func(p *Pool) { p.Spec.Credentials[0].Route = &Route{} },
		"only valid for inject",
	},
	{
		"inject with ttl",
		func(p *Pool) { p.Spec.Credentials[1].TTL = dur(time.Hour) },
		"only valid for mint",
	},
	{
		"inject with app id",
		func(p *Pool) { p.Spec.Credentials[1].AppID = "1" },
		"only valid for mint",
	},
	{"inject bad secret", func(p *Pool) { p.Spec.Credentials[1].Secret = "" }, "secret"},
	{"inject no route", func(p *Pool) { p.Spec.Credentials[1].Route = nil }, "route: is required"},
	{
		"http upstream",
		func(p *Pool) { p.Spec.Credentials[1].Route.Upstream = "http://api.anthropic.com" },
		"https",
	},
	{
		"upstream userinfo",
		func(p *Pool) { p.Spec.Credentials[1].Route.Upstream = "https://u:p@x.com" },
		"https",
	},
	{
		"upstream ip",
		func(p *Pool) { p.Spec.Credentials[1].Route.Upstream = "https://10.0.0.1" },
		"use cidr",
	},
	{
		"upstream allowed",
		func(p *Pool) { allow(p, AllowRule{Host: "*.anthropic.com"}) },
		"must not also be",
	},
	{"reserved header", func(p *Pool) { p.Spec.Credentials[1].Route.Header = "Host" }, wantHeader},
	{"bad header", func(p *Pool) { p.Spec.Credentials[1].Route.Header = "x key" }, wantHeader},
	{
		"duplicate route",
		func(p *Pool) { p.Spec.Credentials[2].Route.Name = "anthropic" },
		"duplicate route",
	},
	{"no agents", func(p *Pool) { p.Spec.Agents = nil }, "at least one agent"},
	{
		"bad agent name",
		func(p *Pool) { p.Spec.Agents["Bad"] = Agent{Command: []string{"x"}} },
		"spec.agents.Bad",
	},
	{"empty command", func(p *Pool) { setAgent(p, func(a *Agent) { a.Command = nil }) }, "command"},
	{"undeclared credential", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Credentials = append(a.Credentials, "aws") })
	}, "undeclared credential"},
	{
		"bad env name",
		func(p *Pool) { setAgent(p, func(a *Agent) { a.Env["1X"] = "v" }) },
		"invalid secret name",
	},
	{
		"proxy env",
		func(p *Pool) { setAgent(p, func(a *Agent) { a.Env["https_proxy"] = "x" }) },
		"set by the pool",
	},
	{"unknown placeholder", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{secret.github}}" })
	}, "unknown placeholder"},
	{"undeclared route", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{ route.nope }}" })
	}, "undeclared route"},
	{"route not granted", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{route.openai}}" })
	}, "does not list credential"},
	{"zero retention", func(p *Pool) { p.Spec.Audit.RetainDays = -1 }, "retainDays"},
	{"relative audit dir", func(p *Pool) { p.Spec.Audit.Dir = "audit" }, "absolute"},
}

func dur(d time.Duration) Duration {
	return Duration{Duration: d}
}

func allow(p *Pool, rule AllowRule) {
	p.Spec.Egress.Allow = append(p.Spec.Egress.Allow, rule)
}

func setAgent(p *Pool, mutate func(a *Agent)) {
	agent := p.Spec.Agents["claude"]
	mutate(&agent)
	p.Spec.Agents["claude"] = agent
}
