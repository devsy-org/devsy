package poolconfig

import "time"

const (
	wantHeader = "route.header"
	wantRepo   = "owner/repo"
)

var credentialRejections = []rejection{
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
		wantRepo,
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
		"upstream port",
		func(p *Pool) { p.Spec.Credentials[1].Route.Upstream = "https://x.com:99999" },
		"outside",
	},
	{
		"upstream allowed",
		func(p *Pool) { allow(p, AllowRule{Host: "*.anthropic.com"}) },
		"must not also be",
	},
	{"reserved header", func(p *Pool) { p.Spec.Credentials[1].Route.Header = "Host" }, wantHeader},
	{
		"hop-by-hop header",
		func(p *Pool) { p.Spec.Credentials[1].Route.Header = "Connection" },
		wantHeader,
	},
	{"bad header", func(p *Pool) { p.Spec.Credentials[1].Route.Header = "x key" }, wantHeader},
	{
		"prefix crlf",
		func(p *Pool) { p.Spec.Credentials[2].Route.Prefix = "Bearer \r\nX-Evil: 1" },
		"printable",
	},
	{
		"duplicate route",
		func(p *Pool) { p.Spec.Credentials[2].Route.Name = "anthropic" },
		"duplicate route",
	},
	{"undeclared credential", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Credentials = append(a.Credentials, "aws") })
	}, "undeclared credential"},
	{"unknown placeholder", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{secret.github}}" })
	}, "unknown placeholder"},
	{"undeclared route", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{ route.nope }}" })
	}, "undeclared route"},
	{"route not granted", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{route.openai}}" })
	}, "does not list credential"},
	malformedCase("unterminated placeholder", "{{route.anthropic"),
	malformedCase("triple braces", "{{{route.anthropic}}}"),
	malformedCase("single braces", "{route.anthropic}"),
	malformedCase("stray close braces", "{{route.anthropic}}}}"),
	{
		"repos with several owners",
		func(p *Pool) { p.Spec.Credentials[0].Scope.Repositories = []string{"acme/api", "other/web"} },
		"same owner",
	},
	{
		"repo dot dot",
		func(p *Pool) { p.Spec.Credentials[0].Scope.Repositories = []string{"acme/.."} },
		wantRepo,
	},
	{
		"owner dot",
		func(p *Pool) { p.Spec.Credentials[0].Scope.Repositories = []string{"./api"} },
		wantRepo,
	},
	{
		"workflows permission",
		func(p *Pool) { p.Spec.Credentials[0].Scope.Permissions["workflows"] = "write" },
		"not allowed for pool agents",
	},
	{
		"administration permission",
		func(p *Pool) { p.Spec.Credentials[0].Scope.Permissions["administration"] = "read" },
		"not allowed for pool agents",
	},
	upstreamCase("upstream path", "https://api.anthropic.com/v1"),
	upstreamCase("upstream empty query", "https://api.anthropic.com?"),
	upstreamCase("upstream empty fragment", "https://api.anthropic.com#"),
	upstreamCase("upstream empty port", "https://api.anthropic.com:"),
}

func malformedCase(name, value string) rejection {
	return rejection{name, func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = value })
	}, "malformed placeholder"}
}

func upstreamCase(name, upstream string) rejection {
	return rejection{name, func(p *Pool) {
		p.Spec.Credentials[1].Route.Upstream = upstream
	}, "no credentials, path, query or fragment"}
}

func (s *PoolConfigSuite) TestCredentialRejections() {
	s.runRejections(credentialRejections)
}

func (s *PoolConfigSuite) TestCredentialExample() {
	p := s.loadExample()
	s.Equal([]string{DeliverGitHelper, DeliverGHEnv}, p.Spec.Credentials[0].Deliver)
	s.Equal("{{route.anthropic}}", p.Spec.Agents["claude"].Env["ANTHROPIC_BASE_URL"])
}

func (s *PoolConfigSuite) TestEnvValuesAcceptedWithoutPlaceholder() {
	for _, value := range []string{`{"a":1}`, "prefix {{ route.anthropic }} suffix"} {
		p := s.loadExample()
		setAgent(p, func(a *Agent) { a.Env["X"] = value })
		s.NoError(p.Validate(), value)
	}
}

func (s *PoolConfigSuite) TestEnvErrorsDoNotEchoValue() {
	for _, value := range []string{"{{CANARY}}", "{{route.CANARY}}", "x {{ route.CANARY }}"} {
		p := s.loadExample()
		setAgent(p, func(a *Agent) { a.Env["X"] = value })
		err := p.Validate()
		s.Require().Error(err)
		s.NotContains(err.Error(), "CANARY")
	}
}

func (s *PoolConfigSuite) TestMintDefaults() {
	p := s.loadExample()
	p.Spec.Credentials[0].TTL = Duration{}
	p.Spec.Credentials[0].Deliver = nil
	p.ApplyDefaults()
	s.Equal(time.Hour, p.Spec.Credentials[0].TTL.Duration)
	s.Equal([]string{DeliverGitHelper}, p.Spec.Credentials[0].Deliver)
	s.Zero(p.Spec.Credentials[1].TTL.Duration)
}

func (s *PoolConfigSuite) TestSecretRefErrorsDoNotEchoValue() {
	for _, raw := range []string{"sk-ant-api03-CANARY value", "sk:ant/CANARY/x"} {
		p := s.loadExample()
		p.Spec.Credentials[1].Secret = raw
		p.Spec.Credentials[0].PrivateKey = raw
		err := p.Validate()
		s.Require().ErrorContains(err, "TYPE:SOURCE/NAME")
		s.NotContains(err.Error(), "CANARY")
	}
}

func (s *PoolConfigSuite) TestScopeReportsRepositoriesAndPermissions() {
	p := s.loadExample()
	p.Spec.Credentials[0].Scope = &MintScope{}
	err := p.Validate()
	s.ErrorContains(err, "scope.repositories")
	s.ErrorContains(err, "scope.permissions")
}
