package poolconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
)

const (
	wantHeader = "route.header"
	wantRepo   = "owner/repo"
)

var credentialRejections = []rejection{
	{
		"duplicate credential",
		func(p *Pool) { p.Spec.Credentials[1].Name = credGitHub },
		"spec.credentials[1].name: duplicates another credential name",
	},
	{"unknown mode", func(p *Pool) { p.Spec.Credentials[0].Mode = "static" }, "mode"},
	{
		"no githubApp",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp = nil },
		"githubApp: is required",
	},
	{"bad app id", func(p *Pool) { p.Spec.Credentials[0].GitHubApp.AppID = "12a" }, "numeric"},
	{
		"bad private key ref",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.PrivateKey = "sops:/x" },
		"privateKey",
	},
	{
		"no scope",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope = nil },
		"scope: is required",
	},
	{
		"bad repo",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Repositories = []string{"api"} },
		wantRepo,
	},
	{
		"no permissions",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Permissions = nil },
		"permission",
	},
	{
		"admin permission",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Permissions["contents"] = "admin" },
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
		func(p *Pool) { p.Spec.Credentials[1].GitHubApp = &GitHubApp{AppID: "1"} },
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
		"port must be a plain number",
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
		"long prefix",
		func(p *Pool) { p.Spec.Credentials[2].Route.Prefix = strings.Repeat("a", 65) },
		"spec.credentials[2].route.prefix: must be at most 64",
	},
	{
		"duplicate route",
		func(p *Pool) { p.Spec.Credentials[2].Route.Name = credAnthropic },
		"spec.credentials[2].route.name: duplicates another route name",
	},
	{"undeclared credential", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Credentials = append(a.Credentials, "aws") })
	}, "must reference a declared credential"},
	{"unknown placeholder", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{secret.github}}" })
	}, "unknown placeholder"},
	{"undeclared route", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{ route.nope }}" })
	}, "undeclared route"},
	{"route not granted", func(p *Pool) {
		setAgent(p, func(a *Agent) { a.Env["X"] = "{{route.openai}}" })
	}, "does not list"},
	malformedCase("unterminated placeholder", "{{route.anthropic"),
	malformedCase("triple braces", "{{{route.anthropic}}}"),
	malformedCase("single braces", "{route.anthropic}"),
	malformedCase("stray close braces", "{{route.anthropic}}}}"),
	{
		"repos with several owners",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Repositories = []string{repoAPI, "other/web"} },
		"same owner",
	},
	{
		"repo dot dot",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Repositories = []string{"acme/.."} },
		wantRepo,
	},
	{
		"owner dot",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Repositories = []string{"./api"} },
		wantRepo,
	},
	{
		"workflows permission",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Permissions["workflows"] = permWrite },
		wantDeniedPermission,
	},
	{
		"administration permission",
		func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Permissions["administration"] = permRead },
		wantDeniedPermission,
	},
	upstreamCase("upstream path", "https://api.anthropic.com/v1"),
	upstreamCase("upstream trailing slash", "https://api.anthropic.com/"),
	upstreamCase("upstream empty query", "https://api.anthropic.com?"),
	upstreamCase("upstream empty fragment", "https://api.anthropic.com#"),
	upstreamCase("upstream empty port", "https://api.anthropic.com:"),
}

func (s *PoolConfigSuite) TestSecretDecodeErrorsDoNotEchoValues() {
	data, err := os.ReadFile("testdata/pool.yaml")
	s.Require().NoError(err)
	for _, field := range []string{"secret: sops:pool/ANTHROPIC_API_KEY", "privateKey: sops:pool/GITHUB_APP_KEY"} {
		name, _, _ := strings.Cut(field, ":")
		for _, value := range []string{"!!null CANARY", "{CANARY: &a 1}", "[CANARY]"} {
			_, err := Parse([]byte(strings.Replace(string(data), field, name+": "+value, 1)))
			s.Require().Error(err, value)
			s.NotContains(err.Error(), "CANARY")
		}
	}
}

// Pins the policy version of testdata/pool.yaml. Field renames, reordering
// and always-serialized new fields change it, and with it the version of
// existing pools that use those fields.
func (s *PoolConfigSuite) TestPolicyVersionGolden() {
	v, err := s.loadExample().PolicyVersion()
	s.Require().NoError(err)
	s.Equal("sha256:6e50a9585c2b974c9e766c3a54ddeed33c4d4edf1a51acc2f74236a2aab18cb2", v)
}

func (s *PoolConfigSuite) TestCredentialErrorsDoNotEchoValues() {
	p := s.loadExample()
	p.Spec.Credentials[0].Deliver = []string{"CANARY_DELIVERY"}
	p.Spec.Credentials[0].GitHubApp.Scope.Repositories = []string{repoAPI, "CANARY_REPO"}
	p.Spec.Credentials[1].Route.Upstream = "https://api.anthropic.com:987654321"
	p.Spec.Credentials[2].Route.Upstream = "https://canary-host.example.com"
	setAgent(p, func(a *Agent) { a.Credentials = append(a.Credentials, "CANARY_CREDENTIAL") })
	p.Spec.Credentials[0].GitHubApp.Scope.Permissions["ghp_CANARY_PERMISSION"] = permRead
	allow(p, AllowRule{Host: "canary-host.example.com"})
	err := p.Validate()
	s.ErrorContains(err, "deliver[0]: unknown target")
	s.ErrorContains(err, "repositories[1]: must be owner/repo")
	s.ErrorContains(err, "port must be a plain number")
	s.ErrorContains(err, "must not also be matched")
	s.ErrorContains(err, "must reference a declared credential")
	s.ErrorContains(err, wantDeniedPermission)
	for _, v := range []string{"CANARY_DELIVERY", "CANARY_REPO", "987654321", "CANARY"} {
		s.NotContains(err.Error(), v)
	}
	s.NotContains(strings.ReplaceAll(err.Error(), "spec.egress.allow", ""), "canary-host")
}

func (s *PoolConfigSuite) TestCredentialErrorsDoNotEchoNames() {
	p := s.loadExample()
	p.Spec.Credentials[0].GitHubApp.Scope.Permissions["canary_password"] = permRead
	p.Spec.Credentials[0].GitHubApp.Scope.Permissions["issues"] = "canary"
	p.Spec.Credentials[1].Name = "canary-dup"
	p.Spec.Credentials[2].Name = "canary-dup"
	p.Spec.Credentials[2].Route.Name = p.Spec.Credentials[1].Route.Name
	p.Spec.Agents["CANARY_AGENT"] = Agent{
		Command: []string{"a"},
		Env:     map[string]string{"X": "{{route.none}}", "ghp CANARY": "{{x}}"},
	}
	err := p.Validate()
	s.ErrorContains(err, "levels must be read or write")
	s.ErrorContains(err, "duplicates another credential name")
	s.ErrorContains(err, "duplicates another route name")
	s.ErrorContains(err, "spec.agents.<key>.env.<key>: names undeclared route")
	s.NotContains(strings.ToLower(err.Error()), "canary")
}

const (
	wantDeniedPermission = "permissions.<key>: names must be one of actions,"
	permWrite            = "write"
	permRead             = "read"
	permMetadata         = "metadata"
)

func (s *PoolConfigSuite) TestDeniedPermissionVariants() {
	for _, name := range []string{
		"dependabot_secrets", "organization_codespaces_secrets", "repository_hooks",
		"organization_self_hosted_runners", "organization_personal_access_tokens",
		"organization_custom_roles", "members", "organization_custom_org_roles",
		"secret_scanning_alerts", "organization_custom_properties", "organization_user_blocking",
		"organization_copilot_agent_settings", "organization_actions_variables",
		"administration", "workflows", "secrets", "unknown_permission",
	} {
		p := s.loadExample()
		p.Spec.Credentials[0].GitHubApp.Scope.Permissions[name] = permWrite
		s.ErrorContains(p.Validate(), wantDeniedPermission, name)
	}
}

func (s *PoolConfigSuite) TestAllowedPermissions() {
	for _, name := range []string{
		"checks", "contents", "deployments", "discussions", "issues", "merge_queues",
		"packages", "pages", "pull_requests", "repository_projects", "statuses",
	} {
		for _, level := range []string{permRead, permWrite} {
			p := s.loadExample()
			p.Spec.Credentials[0].GitHubApp.Scope.Permissions = map[string]string{name: level}
			s.NoError(p.Validate(), name+" "+level)
		}
	}
	for _, name := range []string{"actions", "attestations", "environments", permMetadata} {
		p := s.loadExample()
		p.Spec.Credentials[0].GitHubApp.Scope.Permissions = map[string]string{name: permRead}
		s.NoError(p.Validate(), name)
		p.Spec.Credentials[0].GitHubApp.Scope.Permissions[name] = permWrite
		s.ErrorContains(
			p.Validate(),
			"permissions.<key>: actions, attestations, environments and metadata allow read only",
			name,
		)
	}
}

func (s *PoolConfigSuite) TestWorkflowsPermissionRefused() {
	s.NotContains(allowedPermissions, "workflows")
	for _, level := range []string{permRead, permWrite} {
		p := s.loadExample()
		p.Spec.Credentials[0].GitHubApp.Scope.Permissions["workflows"] = level
		s.ErrorContains(p.Validate(), wantDeniedPermission, level)
	}
}

func (s *PoolConfigSuite) TestMintRepositoryLimit() {
	p := s.loadExample()
	repos := make([]string, 501)
	for i := range repos {
		repos[i] = fmt.Sprintf("acme/repo-%d", i)
	}
	p.Spec.Credentials[0].GitHubApp.Scope.Repositories = repos[:500]
	s.NoError(p.Validate())
	p.Spec.Credentials[0].GitHubApp.Scope.Repositories = repos
	s.ErrorContains(p.Validate(), "scope.repositories: must list at most 500 repositories")
}

func (s *PoolConfigSuite) TestGrantErrorsGiveLinesWithoutKeys() {
	data, err := os.ReadFile("testdata/pool.yaml")
	s.Require().NoError(err)
	yaml := strings.NewReplacer(
		"credentials: [github, anthropic]", "credentials: [github, CANARY-cred]",
		`env: {ANTHROPIC_BASE_URL: "{{route.anthropic}}"}`,
		`env: {"CANARY key!": "{{route.none}}", ghp_CANARY: "{{x}}"}`,
	).Replace(string(data))
	_, err = Parse([]byte(yaml))
	for _, want := range []string{
		"spec.agents.<key>.credentials[1] (line 49): must reference a declared credential",
		"spec.agents.<key>.env.<key> (line 48): names must start",
		"spec.agents.<key>.env.<key> (line 48): names undeclared route",
		"spec.agents.<key>.env.<key> (line 48): unknown placeholder",
	} {
		s.ErrorContains(err, want)
	}
	s.NotContains(err.Error(), "CANARY")
}

func (s *PoolConfigSuite) TestDuplicateRouteKeepsFirstOwner() {
	p := s.loadExample()
	p.Spec.Credentials[2].Route.Name = p.Spec.Credentials[1].Route.Name
	err := p.Validate()
	s.ErrorContains(err, "spec.credentials[2].route.name: duplicates another route name")
	s.NotContains(err.Error(), "uses a route whose credential")
}

func (s *PoolConfigSuite) TestErrorOrderIsStable() {
	p := s.loadExample()
	for _, name := range []string{"a1", "b2", "c3", "d4", "e5", "f6"} {
		p.Spec.Agents[name] = Agent{Env: map[string]string{"1x": "{{route.nope}}", "2y": "{{"}}
	}
	p.Spec.Credentials[0].GitHubApp.Scope.Permissions = map[string]string{
		"x1": permRead, "x2": permRead, "x3": permRead, "actions": permWrite, "issues": "admin",
	}
	want := p.Validate().Error()
	for range 20 {
		s.Equal(want, p.Validate().Error())
	}
}

func (s *PoolConfigSuite) TestInjectTTLNotHashed() {
	p := s.loadExample()
	//nolint:gosec // G117: secret holds a store reference, not a value
	data, err := json.Marshal(p.Spec.Credentials[1])
	s.Require().NoError(err)
	s.NotContains(string(data), "ttl")
}

func (s *PoolConfigSuite) TestSecretRefsMustBeQualified() {
	for _, ref := range []string{
		"ANTHROPIC_API_KEY", "sk_live_CANARY0123", "vault:pool/CANARY", "ghp_CANARY:src/name",
		"local:pool/CANARY", " sops:pool/CANARY", "sops:pool/CANARY ", "sops:pool", "sops:/CANARY",
		"sops:local/CANARY",
	} {
		p := s.loadExample()
		p.Spec.Credentials[1].Secret = ref
		p.Spec.Credentials[0].GitHubApp.PrivateKey = ref
		err := p.Validate()
		s.Require().
			ErrorContains(err, "spec.credentials[1].secret: must be a TYPE:SOURCE/NAME", ref)
		s.ErrorContains(
			err,
			"spec.credentials[0].githubApp.privateKey: must be a TYPE:SOURCE/NAME",
			ref,
		)
		s.NotContains(err.Error(), "CANARY")
	}
	for _, ref := range []string{"local:local/ANTHROPIC_API_KEY", "sops:team-a/ANTHROPIC_API_KEY"} {
		p := s.loadExample()
		p.Spec.Credentials[1].Secret = ref
		s.NoError(p.Validate(), ref)
	}
}

func (s *PoolConfigSuite) TestSopsLocalRefAgreesWithResolver() {
	src := secrets.NewSOPSDataSource("local", "x.json", "json", nil)
	err := secrets.NewResolver().Register(secrets.LocalSourceName, secrets.SOPSFormatter, src)
	s.Require().ErrorContains(err, "is reserved")
	s.Require().NoError(secrets.ValidateSourceType("team-a", secrets.SOPSFormatter))
	local := secrets.LocalSourceName
	s.Require().NoError(secrets.ValidateSourceType(local, local))
}

func (s *PoolConfigSuite) TestPaddedUpstreamPortsRefused() {
	for _, port := range []string{"0443", "00443", "0", "65536"} {
		p := s.loadExample()
		p.Spec.Credentials[1].Route.Upstream = "https://api.anthropic.com:" + port
		s.ErrorContains(p.Validate(), "route.upstream: port must be a plain number", port)
	}
	for _, port := range []string{"1", "443", "8443", "65535"} {
		p := s.loadExample()
		p.Spec.Credentials[1].Route.Upstream = "https://api.anthropic.com:" + port
		s.NoError(p.Validate(), port)
	}
}

func (s *PoolConfigSuite) TestPermissionErrorsGiveEntryLine() {
	data, err := os.ReadFile("testdata/pool.yaml")
	s.Require().NoError(err)
	block := "permissions:\n            contents: write\n            CANARY_admin: write\n" +
		"            actions: write\n            issues: CANARY"
	yaml := strings.Replace(
		string(data),
		"permissions: {contents: write, pull_requests: write}",
		block,
		1,
	)
	_, err = Parse([]byte(yaml))
	s.Require().Error(err)
	s.ErrorContains(err, "permissions.<key> (line 36): names must be one of")
	s.ErrorContains(err, "permissions.<key> (line 37): actions, attestations")
	s.ErrorContains(err, "permissions.<key> (line 38): levels must be read or write")
	s.NotContains(err.Error(), "CANARY")
}

func (s *PoolConfigSuite) TestCredentialDecodeErrorsDoNotEchoKeys() {
	data, err := os.ReadFile("testdata/pool.yaml")
	s.Require().NoError(err)
	perms := "contents: write,"
	for _, tc := range []struct{ old, new, want string }{
		{"mode: mint", "mode: mint\n      canary_field: x", "spec.credentials[0] (line 28): unknown field"},
		{perms, perms + " canary_password: [x],", "permissions.<key> (line 34): must be a string"},
		{perms, perms + " canary_password: read, canary_password: write,", "permissions (line 34): duplicate"},
	} {
		_, err := Parse([]byte(strings.Replace(string(data), tc.old, tc.new, 1)))
		s.ErrorContains(err, tc.want, tc.new)
		s.NotContains(strings.ToLower(err.Error()), "canary", tc.new)
	}
}

func (s *PoolConfigSuite) TestInvalidRouteNameNotEchoedFromEnv() {
	p := s.loadExample()
	p.Spec.Credentials[2].Route.Name = "CANARY_ROUTE"
	setAgent(p, func(a *Agent) { a.Env["X"] = "{{route.CANARY_ROUTE}}" })
	err := p.Validate()
	s.ErrorContains(err, "route.name")
	s.NotContains(err.Error(), "CANARY")
}

func (s *PoolConfigSuite) TestHeaderAcceptsAnyToken() {
	p := s.loadExample()
	p.Spec.Credentials[1].Route.Header = "X_API_Key"
	s.NoError(p.Validate())
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
	for _, value := range []string{
		`{"a":1}`, `{"nested":{"enabled":true}}`, "prefix {{ route.anthropic }} suffix",
	} {
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
		p.Spec.Credentials[0].GitHubApp.PrivateKey = raw
		err := p.Validate()
		s.Require().ErrorContains(err, "TYPE:SOURCE/NAME")
		s.NotContains(err.Error(), "CANARY")
	}
}

func (s *PoolConfigSuite) TestFlatGitHubAppFieldsRefused() {
	data, err := os.ReadFile("testdata/pool.yaml")
	s.Require().NoError(err)
	yaml := strings.Replace(
		string(data),
		"      githubApp:\n",
		"      issuer: github-app\n      githubApp:\n",
		1,
	)
	_, err = Parse([]byte(yaml))
	s.ErrorContains(err, "spec.credentials[0] (line 28): unknown field")
}

func (s *PoolConfigSuite) TestScopeReportsRepositoriesAndPermissions() {
	p := s.loadExample()
	p.Spec.Credentials[0].GitHubApp.Scope = &MintScope{}
	err := p.Validate()
	s.ErrorContains(err, "scope.repositories")
	s.ErrorContains(err, "scope.permissions")
}
