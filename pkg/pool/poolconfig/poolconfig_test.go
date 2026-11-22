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
