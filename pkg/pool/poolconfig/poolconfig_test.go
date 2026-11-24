package poolconfig

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type PoolConfigSuite struct {
	suite.Suite
}

func TestPoolConfigSuite(t *testing.T) {
	suite.Run(t, new(PoolConfigSuite))
}

func (s *PoolConfigSuite) TestExampleIsValid() {
	p := s.loadExample()
	s.Equal("api-agents", p.Metadata.Name)
	s.Equal(4, p.Spec.Capacity.MaxConcurrent)
	s.Equal(8*time.Hour, p.Spec.Lifecycle.TTL.Duration)
}

func (s *PoolConfigSuite) TestDefaults() {
	spec := s.parseMinimal("").Spec
	s.Equal(1, spec.Capacity.MaxConcurrent)
	s.Equal(0, spec.Capacity.MaxQueued)
	s.Equal(10*time.Minute, spec.Lifecycle.SpawnTimeout.Duration)
	s.Equal(15*time.Minute, spec.Lifecycle.IdleTimeout.Duration)
	s.Equal(8*time.Hour, spec.Lifecycle.TTL.Duration)
	s.Equal(2*time.Hour, spec.Lifecycle.JobTimeout.Duration)
	s.Equal(30, spec.Audit.RetainDays)
}

func (s *PoolConfigSuite) TestZeroDurationTakesDefault() {
	p := s.parseMinimal("  lifecycle: {spawnTimeout: 0s, ttl: null, idleTimeout: ~}\n")
	s.Equal(10*time.Minute, p.Spec.Lifecycle.SpawnTimeout.Duration)
	s.Equal(8*time.Hour, p.Spec.Lifecycle.TTL.Duration)
	s.Equal(15*time.Minute, p.Spec.Lifecycle.IdleTimeout.Duration)
}

func (s *PoolConfigSuite) TestPolicyVersionIgnoresFormattingAndDefaults() {
	compact := `{"apiVersion":"devsy.sh/v1alpha1","kind":"Pool","metadata":{"name":"demo"},` +
		`"spec":{"source":{"repo":"https://github.com/acme/api"},"provider":{"name":"docker"},` +
		`"agents":{"claude":{"command":["claude","-p"]}}}}`
	explicit := `apiVersion: devsy.sh/v1alpha1
kind: Pool
metadata: {name: demo}
spec:
  audit: {retainDays: 30}
  agents:
    claude: {command: [claude, -p]}
  lifecycle: {ttl: 8h, idleTimeout: 15m, spawnTimeout: 10m, jobTimeout: 2h}
  capacity: {maxConcurrent: 1}
  provider: {name: docker}
  source: {repo: "https://github.com/acme/api"}
`
	s.Equal(s.version(compact), s.version(explicit))
}

func (s *PoolConfigSuite) TestPolicyVersionChangesWithPolicy() {
	p := s.loadExample()
	before, err := p.PolicyVersion()
	s.Require().NoError(err)
	p.Spec.Capacity.MaxQueued++
	after, err := p.PolicyVersion()
	s.Require().NoError(err)
	s.NotEqual(before, after)
	s.Regexp(`^sha256:[0-9a-f]{64}$`, after)
}

func (s *PoolConfigSuite) TestDevcontainerPathWithDots() {
	p := s.loadExample()
	p.Spec.Source.DevcontainerPath = ".devcontainer/a..b/devcontainer.json"
	s.NoError(p.Validate())
	p.Spec.Source.DevcontainerPath = "a/../../x.json"
	s.ErrorContains(p.Validate(), "devcontainerPath")
	p.Spec.Source.DevcontainerPath = `.devcontainer\devcontainer.json`
	s.ErrorContains(p.Validate(), "devcontainerPath")
}

func (s *PoolConfigSuite) TestRepoCredentialsRefused() {
	for _, repo := range []string{
		"https://x-access-token:CANARY@github.com/acme/api",
		"https://CANARY@github.com/acme/api",
		"user:CANARY@github.com:acme/api.git",
		"git:https://user:CANARY@github.com/acme/api",
		"user:CANARY@github.com/acme?u=https://x",
		"CANARY-user@github.com/acme/api",
		"git:git:https://CANARY@github.com/acme/api",
		"SSH://CANARY@github.com/acme/api",
		"ssh://CANARY0123456789abcdefghijklmnopqrst@github.com/acme/api",
		"ssh://git@CANARY@github.com/acme/api",
		"git@CANARY@github.com:acme/api",
		"//CANARY@github.com/acme/api",
		"ssh://git:CANARY@github.com/acme/api.git",
	} {
		p := s.loadExample()
		p.Spec.Source.Repo = repo
		err := p.Validate()
		s.ErrorContains(err, "spec.source.repo: must not contain credentials", repo)
		s.NotContains(err.Error(), "CANARY")
	}
	for _, repo := range []string{
		"ssh://git@github.com/acme/api.git", "git@github.com:acme/api.git", "github.com/acme/api@main",
		"git:git@github.com:acme/api.git", "github.com:8443/acme/api@main",
		"git@github.com:project.git@main", "https://github.com/acme/api.git@feature/x",
		"https://git.example.com/scm/@team/repo+tools.git", "file:///work/@team/repo%20tools.git",
		"https://git.example.com/@team/repo.git@main", "git@git.example.com:scm/@team/repo+tools.git",
		"ssh://john.smith@git.example.com/team/repo.git", "ssh://John.Smith@git.example.com/team/repo.git",
	} {
		p := s.loadExample()
		p.Spec.Source.Repo = repo
		s.NoError(p.Validate(), repo)
	}
	p := s.loadExample()
	for _, repo := range []string{"https://github.com/acme/%zz", `https:/\CANARY@github.com/x`} {
		p.Spec.Source.Repo = repo
		err := p.Validate()
		s.ErrorContains(err, "spec.source.repo: must be a valid repository URL", repo)
		s.NotContains(err.Error(), "CANARY")
	}
	for _, repo := range []string{
		"https://github.com/acme/api?CANARY=1", "https://github.com/a/b#CANARY",
		"git@github.com:acme/api.git?key=CANARY", "git:git@github.com:acme/api.git#CANARY",
	} {
		p.Spec.Source.Repo = repo
		err := p.Validate()
		s.ErrorContains(err, "spec.source.repo: must not contain a query or fragment")
		s.NotContains(err.Error(), "CANARY")
	}
}

func (s *PoolConfigSuite) TestStrictParsing() {
	_, err := Parse([]byte("apiVersion: devsy.sh/v1alpha1\nkind: Pool\nspec:\n  egres: {}\n"))
	s.ErrorContains(err, "spec (line 4): unknown field")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: 8 hours\n"))
	s.ErrorContains(err, "parse pool config")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: 8\n"))
	s.ErrorContains(err, "spec.lifecycle.ttl (line 3): must be a duration")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: synthetic-secret-marker\n"))
	s.ErrorContains(err, "spec.lifecycle.ttl")
	s.NotContains(err.Error(), "synthetic-secret-marker")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: !!null synthetic-secret-marker\n"))
	s.ErrorContains(err, "spec.lifecycle.ttl (line 3): must be a duration")
	s.NotContains(err.Error(), "synthetic-secret-marker")
	_, err = Parse(append(s.readMinimal(), []byte("  provider: {name: x}\n")...))
	s.ErrorContains(err, "spec (line 13): duplicate key")
	var d Duration
	err = d.UnmarshalJSON([]byte(`"synthetic-secret-marker"`))
	s.Require().Error(err)
	s.NotContains(err.Error(), "synthetic-secret-marker")
}

func (s *PoolConfigSuite) TestRejectsAmbiguousYAML() {
	cases := []struct{ name, extra, want string }{
		{
			"anchor",
			"      env: &m {A: b}\n",
			"spec.agents.<key>.env (line 13): YAML anchors and aliases",
		},
		{
			"alias",
			"      env: &m {Name: demo}\n    other:\n      command: [x]\n      env: *m\n",
			"YAML anchors and aliases are not supported",
		},
		{"merge key", "      env: {<<: {A: b}}\n", "YAML anchors and aliases are not supported"},
		{
			"tagged key",
			"      env: {!!binary c2VjcmV0LXRva2Vu: x}\n",
			"env (line 13): keys must be strings",
		},
		{"list key", "      env: {[secret-token]: x}\n", "env (line 13): keys must be strings"},
		{
			"unknown text key",
			"      'secret-token here': x\n",
			"spec.agents.<key> (line 13): unknown field",
		},
		{
			"duplicate text key",
			"      env: {'secret-token x': a, 'secret-token x': b}\n",
			"duplicate key",
		},
		{
			"invalid key path",
			"      env: {'secret-token x': [a]}\n",
			"env.<key> (line 13): must be a string",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := Parse(append(s.readMinimal(), []byte(tc.extra)...))
			s.ErrorContains(err, tc.want)
			s.NotContains(err.Error(), "secret-token")
		})
	}
}

func (s *PoolConfigSuite) TestAcceptsQuotedKeys() {
	p, err := Parse([]byte(`apiVersion: devsy.sh/v1alpha1
kind: Pool
metadata: {name: demo}
spec:
  source: {repo: "https://github.com/acme/api"}
  provider:
    name: docker
    options: {"1": a, ON: b, 2026-01-15: c, ...: d, 1_000: e, 0x1f: f, null: g, -: h}
  agents:
    claude: {command: [claude]}
    2agent: {command: [x]}
`))
	s.Require().NoError(err)
	s.Equal(
		map[string]string{
			"1": "a", "ON": "b", "2026-01-15": "c", "...": "d",
			"1_000": "e", "0x1f": "f", "null": "g", "-": "h",
		},
		p.Spec.Provider.Options,
	)
	s.Contains(p.Spec.Agents, "2agent")
}

const wantModeString = "spec.agents.<key>.env.<key> (line 13): must be a string"

var wrongShapeCases = []struct{ name, extra, want string }{
	{
		"list duration",
		"  lifecycle: {ttl: [1h]}\n",
		"spec.lifecycle.ttl (line 13): must be a duration",
	},
	{
		"list env value",
		"      env: {MODE: [synthetic-secret]}\n",
		wantModeString,
	},
	{
		"word number",
		"  capacity: {maxConcurrent: synthetic-secret}\n",
		"spec.capacity.maxConcurrent (line 13): must be a whole number",
	},
	{
		"octal number",
		"  capacity: {maxConcurrent: 010}\n",
		"spec.capacity.maxConcurrent (line 13): must be a whole number",
	},
	{
		"mapping in string list",
		"      command: [a, {synthetic-secret: &x 1}]\n",
		"spec.agents.<key>.command[1] (line 13): must be a string",
	},
	{
		"null-tagged list",
		"      env: {MODE: !!null [synthetic-secret]}\n",
		wantModeString,
	},
	{
		"null-tagged mapping",
		"      env: {MODE: !!null {synthetic-secret: &x 1}}\n",
		wantModeString,
	},
	{"null-tagged text", "      env: {MODE: !!null synthetic-secret}\n", wantModeString},
	{
		"null-tagged command",
		"      command: !!null [claude]\n",
		"command (line 13): must be a list",
	},
	{
		"null-tagged struct",
		"  audit: !!null {retainDays: 3}\n",
		"spec.audit (line 13): must be a mapping",
	},
	{"bool-tagged quoted", "      env: {MODE: !!bool \"yes\"}\n", wantModeString},
	{"binary-tagged", "      env: {MODE: !!binary c3ludGhldGljLXNlY3JldA==}\n", wantModeString},
	{"int-tagged quoted", "      env: {MODE: !!int '0123'}\n", wantModeString},
	{"null-tagged null", "      env: {MODE: !!null \"null\"}\n", wantModeString},
	{
		"null env value",
		"      env: {MODE: null}\n",
		"spec.agents.<key>.env.<key> (line 13): must not be null",
	},
	{
		"null command item",
		"      command: [claude, ~]\n",
		"command[1] (line 13): must not be null",
	},
	{"null agent", "    other: null\n", "spec.agents.<key> (line 13): must not be null"},
}

func (s *PoolConfigSuite) TestRejectsWrongShapes() {
	for _, tc := range wrongShapeCases {
		s.Run(tc.name, func() {
			data, want := string(s.readMinimal()), tc.want
			// Agent fields replace the command line so a command case is not a
			// duplicate key.
			if strings.HasPrefix(tc.extra, "      ") {
				data = strings.Replace(data, "      command: [claude, -p]\n", "", 1)
				want = strings.Replace(want, "line 13", "line 12", 1)
			}
			_, err := Parse([]byte(data + tc.extra))
			s.ErrorContains(err, want)
			s.NotContains(err.Error(), "synthetic-secret")
		})
	}
}

func (s *PoolConfigSuite) TestAcceptsBlockAndLongKeys() {
	long := strings.Repeat("k", 1100)
	p, err := Parse([]byte(`apiVersion: devsy.sh/v1alpha1
kind: Pool
metadata: {name: demo}
spec:
  source: {repo: "https://github.com/acme/api"}
  provider:
    name: docker
    options:
      ? |-
        ON
      : a
      ? ` + long + `
      : b
  agents:
    claude: {command: [claude]}
`))
	s.Require().NoError(err)
	s.Equal(map[string]string{"ON": "a", long: "b"}, p.Spec.Provider.Options)
}

func (s *PoolConfigSuite) TestStringValuesKeepText() {
	for _, value := range []string{"yes", "Off", "0123", "0x1F", "1_000", "+1", "1.5", "~x", "null x"} {
		p := s.parseMinimal("      env: {MODE: " + value + "}\n")
		s.Equal(value, p.Spec.Agents["claude"].Env["MODE"])
	}
	data := strings.Replace(string(s.readMinimal()), "[claude, -p]", "[cat, -, ---, a:b]", 1)
	p, err := Parse([]byte(data +
		"      env:\n        A: \"yes\"\n        B: 8080\n        C: true\n        D: !!str 0123\n" +
		"        E: 2026-01-15\n        F: |-\n          on\n",
	))
	s.Require().NoError(err)
	s.Equal(map[string]string{
		"A": "yes", "B": "8080", "C": "true", "D": "0123", "E": "2026-01-15", "F": "on",
	}, p.Spec.Agents["claude"].Env)
	s.Equal([]string{"cat", "-", "---", "a:b"}, p.Spec.Agents["claude"].Command)
}

func (s *PoolConfigSuite) TestReportsEveryError() {
	_, err := Parse([]byte("apiVersion: v1\nkind: Other\n"))
	s.Require().Error(err)
	for _, want := range []string{
		"apiVersion", "kind", "metadata.name", "spec.source.repo",
		"spec.provider.name", "spec.agents",
	} {
		s.ErrorContains(err, want)
	}
}

func (s *PoolConfigSuite) TestRejectsExtraDocuments() {
	data := s.readMinimal()
	_, err := Parse(append(data, []byte("---\napiVersion: v0\n")...))
	s.ErrorContains(err, "exactly one YAML document")
	_, err = Parse(append([]byte("---\n"), data...))
	s.NoError(err)
}

func (s *PoolConfigSuite) TestRequiresExactFieldNames() {
	_, err := Parse(append(s.readMinimal(), []byte("  Agents: {}\n")...))
	s.ErrorContains(err, "spec (line 13): unknown field")
	_, err = Parse(append(s.readMinimal(), []byte("  audit: {RetainDays: 3}\n")...))
	s.ErrorContains(err, "spec.audit (line 13): unknown field")
	p := s.parseMinimal("      env: {Foo: a, foo: b}\n")
	s.Len(p.Spec.Agents["claude"].Env, 2)
}

func (s *PoolConfigSuite) TestRejections() {
	s.runRejections(rejections)
}

const (
	wantInvalidYAML = "invalid YAML"
	wantEnvField    = "env.<key> (line 13): must be a string"
)

func (s *PoolConfigSuite) TestReservedEnvNames() {
	for _, name := range append(slices.Clone(reservedEnvNames),
		"http_proxy", "GIT_CONFIG_COUNT", "GIT_SSL_NO_VERIFY", "git_ssl_cainfo") {
		p := s.loadExample()
		setAgent(p, func(a *Agent) { a.Env = map[string]string{name: "x"} })
		s.ErrorContains(p.Validate(), "is set by the pool", name)
	}
}

func (s *PoolConfigSuite) TestValidationErrorsGiveLines() {
	_, err := Parse(append(s.readMinimal(), []byte("  capacity: {maxQueued: -1}\n")...))
	s.ErrorContains(err, "spec.capacity.maxQueued (line 13): must not be negative")
	_, err = Parse(append(s.readMinimal(), []byte("      env: {HTTP_PROXY: x}\n")...))
	s.ErrorContains(err, "spec.agents.<key>.env.HTTP_PROXY (line 13): is set by the pool")
	_, err = Parse(append(s.readMinimal(), []byte("      env: {GIT_SSL_CANARY: x}\n")...))
	s.ErrorContains(err, "spec.agents.<key>.env.<key> (line 13): is set by the pool")
	s.NotContains(err.Error(), "CANARY")
	_, err = Parse(append(s.readMinimal(), []byte("      env: {ghp_CANARY: x}\n")...))
	s.NoError(err)
	_, err = Parse(append(s.readMinimal(), []byte("      env: {'CANARY x': y}\n")...))
	s.ErrorContains(err, "spec.agents.<key>.env.<key> (line 13): names must start")
	s.NotContains(err.Error(), "CANARY")
	_, err = Parse(
		append(s.readMinimal(), []byte("    0123456789abcdef0123456789abcdef: {command: []}\n")...),
	)
	s.ErrorContains(err, "spec.agents.<key>.command (line 13): is required")
	s.NotContains(err.Error(), "0123456789abcdef")
	_, err = Parse(append(s.readMinimal(), []byte("    CANARY: {command: [x]}\n")...))
	s.ErrorContains(err, "spec.agents.<key> (line 13): must be 1 to 63")
	s.NotContains(err.Error(), "CANARY")
	bad := strings.Replace(string(s.readMinimal()), "command: [claude, -p]", "command: []", 1)
	_, err = Parse([]byte(bad + "    'claude.command': {command: [x]}\n"))
	s.ErrorContains(err, "spec.agents.<key>.command (line 12): is required")
	p := s.loadExample()
	p.Spec.Capacity.MaxQueued = -1
	s.ErrorContains(p.Validate(), "spec.capacity.maxQueued: must not be negative")
}

func (s *PoolConfigSuite) TestDecoderErrorsDoNotEchoValues() {
	cases := []struct{ name, extra, want string }{
		{"tagged int", "      env: {PASS: !!int CANARY}\n", wantEnvField},
		{"key-shaped token", "      env: {CANARY_TOKEN: [a]}\n", wantEnvField},
		{"unknown field", "      CANARY_field: x\n", "spec.agents.<key> (line 13): unknown field"},
		{"duplicate key", "      env: {CANARY: a, CANARY: b}\n", "env (line 13): duplicate key"},
		{
			"duplicate agent",
			"    CANARY: {command: [a]}\n    CANARY: {command: [b]}\n",
			"spec.agents (line 14): duplicate key",
		},
		{"unknown anchor", "      env: {PASS: *CANARY}\n", wantInvalidYAML},
		{
			"fake field path",
			"      env: {PASS: !!int 'Go struct field X.CANARY of type int'}\n",
			wantEnvField,
		},
		{"fake line", "      env: {PASS: !!int 'line 987654321 CANARY'}\n", wantEnvField},
		{"syntax error", "      env: {PASS: 'CANARY\n", "invalid YAML at line"},
		{
			"overflow",
			"  capacity: {maxQueued: 9223372036854775987}\n",
			"spec.capacity.maxQueued (line 13): must be a whole number",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := Parse(append(s.readMinimal(), []byte(tc.extra)...))
			s.ErrorContains(err, tc.want)
			s.NotContains(err.Error(), "CANARY")
			s.NotContains(err.Error(), "9223372036854775987")
			s.NotContains(err.Error(), "987654321")
		})
	}
}

func (s *PoolConfigSuite) loadExample() *Pool {
	p, err := Load(filepath.Join("testdata", "pool.yaml"))
	s.Require().NoError(err)
	return p
}

func (s *PoolConfigSuite) readMinimal() []byte {
	data, err := os.ReadFile(filepath.Join("testdata", "minimal.yaml"))
	s.Require().NoError(err)
	return data
}

// parseMinimal appends extra, which may extend the last agent or add spec
// keys, to testdata/minimal.yaml.
func (s *PoolConfigSuite) parseMinimal(extra string) *Pool {
	p, err := Parse(append(s.readMinimal(), []byte(extra)...))
	s.Require().NoError(err)
	return p
}

func (s *PoolConfigSuite) version(content string) string {
	p, err := Parse([]byte(content))
	s.Require().NoError(err)
	v, err := p.PolicyVersion()
	s.Require().NoError(err)
	return v
}

func (s *PoolConfigSuite) runRejections(cases []rejection) {
	for _, tc := range cases {
		s.Run(tc.name, func() {
			p := s.loadExample()
			tc.mutate(p)
			s.ErrorContains(p.Validate(), tc.want)
		})
	}
}
