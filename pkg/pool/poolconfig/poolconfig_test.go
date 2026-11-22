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
	p := s.parseMinimal("  lifecycle: {spawnTimeout: 0s}\n")
	s.Equal(10*time.Minute, p.Spec.Lifecycle.SpawnTimeout.Duration)
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
}

func (s *PoolConfigSuite) TestStrictParsing() {
	_, err := Parse([]byte("apiVersion: devsy.sh/v1alpha1\nkind: Pool\nspec:\n  egres: {}\n"))
	s.ErrorContains(err, "unknown field")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: 8 hours\n"))
	s.ErrorContains(err, "parse pool config")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: 8\n"))
	s.ErrorContains(err, "spec.lifecycle.ttl: must be a duration")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: synthetic-secret-marker\n"))
	s.ErrorContains(err, "spec.lifecycle.ttl")
	s.NotContains(err.Error(), "synthetic-secret-marker")
	_, err = Parse([]byte("spec:\n  lifecycle:\n    ttl: !!null synthetic-secret-marker\n"))
	s.ErrorContains(err, "spec.lifecycle.ttl: must be a duration")
	s.NotContains(err.Error(), "synthetic-secret-marker")
	var d Duration
	err = d.UnmarshalJSON([]byte(`"synthetic-secret-marker"`))
	s.Require().Error(err)
	s.NotContains(err.Error(), "synthetic-secret-marker")
}

func (s *PoolConfigSuite) TestRejectsAmbiguousYAML() {
	cases := []struct{ name, extra, want string }{
		{"anchor", "      env: &m {A: b}\n", "spec.agents.claude.env: YAML anchors and aliases"},
		{
			"alias",
			"      env: &m {Name: demo}\n    other:\n      command: [x]\n      env: *m\n",
			"YAML anchors and aliases are not supported",
		},
		{"merge key", "      env: {<<: {A: b}}\n", "YAML anchors and aliases are not supported"},
		{
			"numeric key",
			"      env: {1: a}\n",
			`spec.agents.claude.env: key "1" must be a quoted string`,
		},
		{"bool word key", "      env: {ON: x}\n", `key "ON" must be a quoted string`},
		{"null key", "      env: {null: secret-token}\n", `key "null" must be a quoted string`},
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
  provider: {name: docker, options: {"1": a, "ON": b}}
  agents:
    claude: {command: [claude]}
`))
	s.Require().NoError(err)
	s.Equal(map[string]string{"1": "a", "ON": "b"}, p.Spec.Provider.Options)
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
	s.ErrorContains(err, `spec: unknown field "Agents"`)
	_, err = Parse(append(s.readMinimal(), []byte("  audit: {RetainDays: 3}\n")...))
	s.ErrorContains(err, `spec.audit: unknown field "RetainDays"`)
	p := s.parseMinimal("      env: {Foo: a, foo: b}\n")
	s.Len(p.Spec.Agents["claude"].Env, 2)
}

func (s *PoolConfigSuite) TestRejections() {
	s.runRejections(rejections)
}

func (s *PoolConfigSuite) TestDecoderErrorsDoNotEchoValues() {
	cases := []struct{ name, extra, want string }{
		{"tagged int", "      env: {PASS: !!int CANARY}\n", "invalid YAML"},
		{"unknown anchor", "      env: {PASS: *CANARY}\n", "invalid YAML"},
		{
			"overflow",
			"  capacity: {maxQueued: 9223372036854775987}\n",
			"spec.capacity.maxQueued: must be of type int",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := Parse(append(s.readMinimal(), []byte(tc.extra)...))
			s.ErrorContains(err, tc.want)
			s.NotContains(err.Error(), "CANARY")
			s.NotContains(err.Error(), "9223372036854775987")
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
