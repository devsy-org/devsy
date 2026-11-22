package poolconfig

import (
	"os"
	"path/filepath"
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
	s.Equal([]int{443}, p.Spec.Egress.Allow[0].Ports)
	s.Equal([]string{DeliverGitHelper, DeliverGHEnv}, p.Spec.Credentials[0].Deliver)
}

func (s *PoolConfigSuite) TestDefaults() {
	data, err := os.ReadFile(filepath.Join("testdata", "minimal.yaml"))
	s.Require().NoError(err)
	p, err := Parse(data)
	s.Require().NoError(err)
	spec := p.Spec
	s.Equal(1, spec.Capacity.MaxConcurrent)
	s.Equal(0, spec.Capacity.MaxQueued)
	s.Equal(10*time.Minute, spec.Lifecycle.SpawnTimeout.Duration)
	s.Equal(15*time.Minute, spec.Lifecycle.IdleTimeout.Duration)
	s.Equal(8*time.Hour, spec.Lifecycle.TTL.Duration)
	s.Equal(2*time.Hour, spec.Lifecycle.JobTimeout.Duration)
	s.Equal(EgressDeny, spec.Egress.Default)
	s.Equal(30, spec.Audit.RetainDays)
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
  egress: {default: deny}
  provider: {name: docker}
  source: {repo: "https://github.com/acme/api"}
`
	s.Equal(s.version(compact), s.version(explicit))
}

func (s *PoolConfigSuite) TestPolicyVersionChangesWithPolicy() {
	p := s.loadExample()
	before, err := p.PolicyVersion()
	s.Require().NoError(err)
	allow(p, AllowRule{Host: "example.com", Ports: []int{443}})
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
}

func (s *PoolConfigSuite) TestStrictParsing() {
	_, err := Parse([]byte("apiVersion: devsy.sh/v1alpha1\nkind: Pool\nspec:\n  egres: {}\n"))
	s.ErrorContains(err, "unknown field")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: 8 hours\n"))
	s.ErrorContains(err, "parse pool config")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: 8\n"))
	s.ErrorContains(err, "duration must be a string")
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

func (s *PoolConfigSuite) TestRejections() {
	for _, tc := range rejections {
		s.Run(tc.name, func() {
			p := s.loadExample()
			tc.mutate(p)
			s.ErrorContains(p.Validate(), tc.want)
		})
	}
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

func (s *PoolConfigSuite) TestRejectsExtraDocuments() {
	data, err := os.ReadFile(filepath.Join("testdata", "minimal.yaml"))
	s.Require().NoError(err)
	_, err = Parse(append(data, []byte("---\napiVersion: v0\n")...))
	s.ErrorContains(err, "exactly one YAML document")
	_, err = Parse(append([]byte("---\n"), data...))
	s.NoError(err)
}

func (s *PoolConfigSuite) TestRequiresExactFieldNames() {
	data, err := os.ReadFile(filepath.Join("testdata", "minimal.yaml"))
	s.Require().NoError(err)
	_, err = Parse(append(data, []byte("  Agents: {}\n")...))
	s.ErrorContains(err, `spec: unknown field "Agents"`)
	_, err = Parse(append(data, []byte("  egress: {allow: [{Host: a.com}]}\n")...))
	s.ErrorContains(err, `spec.egress.allow: unknown field "Host"`)
	p, err := Parse(append(data, []byte("      env: {Foo: a, foo: b}\n")...))
	s.Require().NoError(err)
	s.Len(p.Spec.Agents["claude"].Env, 2)
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

func (s *PoolConfigSuite) TestZeroDurationTakesDefault() {
	data, err := os.ReadFile(filepath.Join("testdata", "minimal.yaml"))
	s.Require().NoError(err)
	p, err := Parse(append(data, []byte("  lifecycle: {spawnTimeout: 0s}\n")...))
	s.Require().NoError(err)
	s.Equal(10*time.Minute, p.Spec.Lifecycle.SpawnTimeout.Duration)
}

func (s *PoolConfigSuite) loadExample() *Pool {
	p, err := Load(filepath.Join("testdata", "pool.yaml"))
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
