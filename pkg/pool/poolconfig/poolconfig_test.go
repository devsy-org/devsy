package poolconfig

import (
	"encoding/json"
	"net/netip"
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
	for name, mutate := range map[string]func(*Pool){
		"egress": func(p *Pool) { allow(p, AllowRule{Host: "x.example.com", Ports: []int{443}}) },
		"env":    func(p *Pool) { setAgent(p, func(a *Agent) { a.Env["MODE"] = "fast" }) },
		"repo":   func(p *Pool) { p.Spec.Source.Repo = "https://github.com/acme/web" },
		"ttl":    func(p *Pool) { p.Spec.Lifecycle.TTL = dur(time.Hour) },
		"idle":   func(p *Pool) { p.Spec.Lifecycle.IdleTimeout = dur(time.Minute) },
		"job":    func(p *Pool) { p.Spec.Lifecycle.JobTimeout = dur(time.Minute) },
		"spawn":  func(p *Pool) { p.Spec.Lifecycle.SpawnTimeout = dur(time.Minute) },
		"perm":   func(p *Pool) { p.Spec.Credentials[0].GitHubApp.Scope.Permissions["issues"] = permRead },
	} {
		policy, config := s.versions(s.loadExample())
		p := s.loadExample()
		mutate(p)
		newPolicy, newConfig := s.versions(p)
		s.NotEqual(policy, newPolicy, name)
		s.NotEqual(config, newConfig, name)
		s.Regexp(`^sha256:[0-9a-f]{64}$`, newPolicy)
	}
}

func (s *PoolConfigSuite) TestOperationalChangesKeepPolicyVersion() {
	for name, mutate := range map[string]func(*Pool){
		"capacity":  func(p *Pool) { p.Spec.Capacity.MaxQueued++ },
		"audit dir": func(p *Pool) { p.Spec.Audit.Dir = "/var/log/pool" },
		"retain":    func(p *Pool) { p.Spec.Audit.RetainDays++ },
		"name":      func(p *Pool) { p.Metadata.Name = "other" },
	} {
		policy, config := s.versions(s.loadExample())
		p := s.loadExample()
		mutate(p)
		newPolicy, newConfig := s.versions(p)
		s.Equal(policy, newPolicy, name)
		s.NotEqual(config, newConfig, name)
	}
}

func (s *PoolConfigSuite) TestVersionsIgnoreSetOrderAndDuplicates() {
	policy, config := s.versions(s.loadExample())
	p := s.loadExample()
	p.Spec.Egress.Allow[2].Ports = []int{22, 443, 22}
	c := &p.Spec.Credentials[0]
	c.Deliver = []string{DeliverGHEnv, DeliverGitHelper, DeliverGHEnv}
	c.GitHubApp.Scope.Repositories = []string{repoWeb, "ACME/API", repoWeb}
	setAgent(p, func(a *Agent) { a.Credentials = []string{credAnthropic, credGitHub, credGitHub} })
	q := s.loadExample()
	q.Spec.Credentials[0].GitHubApp.Scope.Repositories = []string{repoAPI, repoWeb}
	wantPolicy, wantConfig := s.versions(q)
	gotPolicy, gotConfig := s.versions(p)
	s.Equal(wantPolicy, gotPolicy)
	s.Equal(wantConfig, gotConfig)
	s.NotEqual(policy, gotPolicy)
	s.NotEqual(config, gotConfig)
	s.Equal(
		[]int{22, 443, 22},
		p.Spec.Egress.Allow[2].Ports,
		"normalizing must not change the pool",
	)
}

func (s *PoolConfigSuite) TestVersionsDoNotChangeThePool() {
	p := s.loadExample()
	p.Spec.Egress.Allow[2].Ports = []int{443, 22, 443}
	p.Spec.Credentials[0].Deliver = []string{DeliverGHEnv, DeliverGitHelper, DeliverGHEnv}
	p.Spec.Credentials[0].GitHubApp.Scope.Repositories = []string{repoWeb, "ACME/API", repoWeb}
	setAgent(p, func(a *Agent) { a.Credentials = []string{credGitHub, credAnthropic, credGitHub} })
	before, err := json.Marshal(p)
	s.Require().NoError(err)
	_, _ = s.versions(p)
	after, err := json.Marshal(p)
	s.Require().NoError(err)
	s.JSONEq(string(before), string(after))
}

func (s *PoolConfigSuite) TestOrderSensitiveListsChangeVersions() {
	for name, mutate := range map[string]func(*Pool){
		"allow order": func(p *Pool) {
			a := p.Spec.Egress.Allow
			a[0], a[1] = a[1], a[0]
		},
		"command order": func(p *Pool) {
			setAgent(p, func(a *Agent) { a.Command[1], a.Command[2] = a.Command[2], a.Command[1] })
		},
	} {
		policy, config := s.versions(s.loadExample())
		p := s.loadExample()
		mutate(p)
		newPolicy, newConfig := s.versions(p)
		s.NotEqual(policy, newPolicy, name)
		s.NotEqual(config, newConfig, name)
	}
}

func (s *PoolConfigSuite) TestEquivalentCIDRSpellingsShareVersions() {
	a, b := s.loadExample(), s.loadExample()
	allow(a, AllowRule{CIDR: "2001:db8::/32"})
	allow(b, AllowRule{CIDR: "2001:0db8:0:0:0:0:0:0/32"})
	policyA, configA := s.versions(a)
	policyB, configB := s.versions(b)
	s.Equal(policyA, policyB)
	s.Equal(configA, configB)
}

func (s *PoolConfigSuite) TestDuplicateCIDRSpellingsRefused() {
	p := s.loadExample()
	allow(p, AllowRule{CIDR: "2001:db8::/32"})
	allow(p, AllowRule{CIDR: "2001:0db8:0:0:0:0:0:0/32"})
	s.ErrorContains(p.Validate(), "spec.egress.allow[5]: duplicates another rule's host or cidr")
}

func (s *PoolConfigSuite) TestDuplicateItemsGiveLines() {
	extra := "  egress:\n    allow:\n      - host: a.example.com\n        ports: [443, 443]\n"
	_, err := Parse(append(s.readMinimal(), []byte(extra)...))
	s.Require().
		ErrorContains(err, "spec.egress.allow[0].ports[1] (line 16): duplicates another port")
}

func (s *PoolConfigSuite) TestDuplicateListItemsRefused() {
	p := s.loadExample()
	p.Spec.Egress.Allow[2].Ports = []int{443, 22, 443}
	allow(p, AllowRule{Host: "github.com", Ports: []int{80}})
	allow(p, AllowRule{CIDR: "10.20.0.0/16"})
	c := &p.Spec.Credentials[0]
	c.Deliver = []string{DeliverGitHelper, DeliverGitHelper}
	c.GitHubApp.Scope.Repositories = []string{repoAPI, "Acme/API"}
	setAgent(p, func(a *Agent) { a.Credentials = []string{credGitHub, credAnthropic, credGitHub} })
	err := p.Validate()
	s.Require().Error(err)
	for _, want := range []string{
		"spec.egress.allow[2].ports[2]: duplicates another port",
		"spec.egress.allow[4]: duplicates another rule's host or cidr",
		"spec.egress.allow[5]: duplicates another rule's host or cidr",
		"spec.credentials[0].deliver[1]: duplicates another target",
		"spec.credentials[0].githubApp.scope.repositories[1]: duplicates another repository",
		"spec.agents.<key>.credentials[2]: duplicates another credential",
	} {
		s.ErrorContains(err, want)
	}
}

func (s *PoolConfigSuite) TestSourceRef() {
	for _, ref := range []string{
		"main", "feature/x", "v1.2.3", "refs/heads/main", "release-1.0", "0123456789abcdef0123456789abcdef01234567",
		"a@b", "x/@", "a{b",
	} {
		p := s.loadExample()
		p.Spec.Source.Ref = ref
		s.NoError(p.Validate(), ref)
	}
	for _, ref := range []string{
		"-CANARY", "--upload-pack=CANARY", "CANARY..x", "CANARY x", "CANARY~1", "CANARY^", "CANARY:x",
		"CANARY?", "CANARY*", "CANARY[", `CANARY\x`, "CANARY/", "/CANARY", "CANARY//x", ".CANARY",
		"CANARY/.x", "CANARY.lock", "CANARY/x.lock/y", "CANARY.", "@", "CANARY@{1}", "CANARY\tx", "CANARY\x7f",
	} {
		p := s.loadExample()
		p.Spec.Source.Ref = ref
		err := p.Validate()
		s.ErrorContains(err, "spec.source.ref: must be a valid git ref name", ref)
		s.NotContains(err.Error(), "CANARY")
	}
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

var repoTargetRejections = map[string][]string{
	errRepoURL: {
		"--upload-pack=touch",
		"-oProxyCommand=x",
		"ssh://-oProxyCommand=sh/x",
		"git@-oProxyCommand=x:p",
		"git:--upload-pack=touch",
		"https://github.com/-acme/api",
		"https://github.com//-acme/api",
		"git@github.com:-acme/api",
		"git@github.com:/-acme/api",
		"https://127.1/acme/api",
		"https://0x7f.1/acme/api",
		"https://2130706433/acme/api",
		"git@[::1/acme/api",
		"https://ex_ample.com/x",
		"https://a..example.com/x",
		"https://\uff11\uff12\uff17.1/x",
		"https://github.com:0/acme/api",
		"https://github.com:65536/acme/api",
		"ssh://git@github.com:99999/acme/api",
		"ssh://git@github.com:0/acme/api",
	},
	errRepoScheme: {
		"file:///etc", "http://169.254.169.254/x", "http://github.com/acme/api",
		"file:///work/@team/repo%20tools.git",
	},
	errRepoHost: {
		"https://localhost/x",
		"https://intranet/acme/api",
		"git@gitserver:acme/api",
		"https://gitlab.example.internal/acme/api",
		"https://git.corp.local/acme/api",
		"https://git.svc/acme/api",
		"https://git.default.svc.cluster.local/acme/api",
		"https://git.corp.localdomain/acme/api",
		"https://git.home.arpa/acme/api",
		"https://x.metadata.goog/acme/api",
		"https://LocalHost./x",
		"https://api.localhost/x",
		"https://169.254.169.254/x",
		"https://[::ffff:169.254.169.254]/x",
		"https://[::ffff:a9fe:a9fe]/x",
		"https://[::1]/x",
		"https://[fe80::1%25eth0]/x",
		"https://[fd00:ec2::254]/x",
		"https://10.0.0.1/x",
		"https://172.16.0.1/x",
		"https://192.168.1.1:8443/x",
		"https://100.64.0.1/x",
		"https://100.100.100.200/x",
		"https://168.63.129.16/x",
		"https://0.0.0.0/x",
		"https://255.255.255.255/x",
		"https://224.0.0.1/x",
		"https://[64:ff9b::a9fe:a9fe]/x",
		"https://[2002:a9fe:a9fe::]/x",
		"https://[::a9fe:a9fe]/x",
		"https://metadata.google.internal/x",
		"https://METADATA.google.internal./x",
		"https://metadata/x",
		"ssh://git@instance-data/x",
		"git@localhost:acme/api",
		"git@[::1]:acme/api",
		"git@127.0.0.1:acme/api",
		"ssh://git@192.168.1.1/acme/api",
		"192.168.1.1/acme/api",
		"https://[3fff::1]/x",
		"https://localhost\u3002/x",
		"https://localhost\uff0e/x",
		"https://l\uff4fcalhost/x",
		"https://local\u00adhost/x",
		"https://localhost\u200b/x",
		"https://metadata.google.internal\u3002/x",
		"git@localhost\u3002:x",
		"https://127\u30020\u30020\u30021/x",
	},
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 0x80 >> (i % 8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

func repoURLFor(a netip.Addr) string {
	if a.Is4() {
		return "https://" + a.String() + "/acme/api"
	}
	return "https://[" + a.String() + "]/acme/api"
}

// The first and last address of each range must stay refused, and the
// addresses just outside must stay allowed, so a widened or dropped range
// fails here.
func (s *PoolConfigSuite) TestRepoBlockedRangesPinned() {
	pinned := map[string]string{
		"fec0::/10":       "",
		"2001::/23":       "2001:200::1",
		"100::/64":        "100:0:0:1::1",
		"198.18.0.0/15":   "198.20.0.1",
		"64:ff9b:1::/48":  "64:ff9b:2::1",
		"::ffff:0:0:0/96": "0:0:0:fffe::1",
		"192.0.0.0/24":    "192.0.1.1",
	}
	for cidr, outside := range pinned {
		prefix := netip.MustParsePrefix(cidr)
		s.Contains(blockedRepoPrefixes, prefix, cidr)
		for _, a := range []netip.Addr{prefix.Masked().Addr(), lastAddr(prefix)} {
			p := s.loadExample()
			p.Spec.Source.Repo = repoURLFor(a)
			s.ErrorContains(p.Validate(), "spec.source.repo: "+errRepoHost, a.String())
		}
		if outside != "" {
			p := s.loadExample()
			p.Spec.Source.Repo = repoURLFor(netip.MustParseAddr(outside))
			s.NoError(p.Validate(), outside)
		}
	}
	p := s.loadExample()
	p.Spec.Source.Repo = "https://198.17.255.255/acme/api"
	s.NoError(p.Validate())
}

func (s *PoolConfigSuite) TestRepoTargetRefused() {
	for want, repos := range repoTargetRejections {
		for _, repo := range repos {
			p := s.loadExample()
			p.Spec.Source.Repo = repo
			err := p.Validate()
			s.Require().ErrorContains(err, "spec.source.repo: "+want, repo)
			s.NotContains(err.Error(), repo)
		}
	}
	for _, repo := range []string{
		"https://8.8.8.8/acme/api", "https://[2606:4700::1111]/acme/api", "git@[2606:4700::1111]:acme/api",
		"ssh://git@github.com:22/acme/api", "https://github.com./acme/api", "https://github.com/acme/-api",
		"https://github.com:1/acme/api", "https://github.com:65535/acme/api",
		"https://internal.example.com/acme/api", "https://example.\u307f\u3093\u306a/acme/api",
		"https://example.xn--q9jyb4c/acme/api", "https://\u212aube.example.com/acme/api",
	} {
		p := s.loadExample()
		p.Spec.Source.Repo = repo
		s.NoError(p.Validate(), repo)
	}
}

func (s *PoolConfigSuite) TestSourceRefParseErrorGivesLine() {
	data, err := os.ReadFile("testdata/pool.yaml")
	s.Require().NoError(err)
	_, err = Parse([]byte(strings.Replace(string(data), "ref: main", "ref: CANARY..x", 1)))
	s.Require().ErrorContains(err, "spec.source.ref (line 8): must be a valid git ref name")
	s.NotContains(err.Error(), "CANARY")
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
		"ssh://Mixed.Case.Login@gitlab.com/acme/api", "ssh://John.CANARY@github.com/acme/api",
		"ssh://CANARY%4@github.com/acme/api", "ssh://%43ANARY@github.com/acme/api",
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
		"https://git.example.com/scm/@team/repo+tools.git",
		"https://git.example.com/@team/repo.git@main", "git@git.example.com:scm/@team/repo+tools.git",
		"ssh://john.smith@git.example.com/team/repo.git", "ssh://john%2Esmith@git.example.com/team/repo.git",
	} {
		p := s.loadExample()
		p.Spec.Source.Repo = repo
		s.NoError(p.Validate(), repo)
	}
	p := s.loadExample()
	for _, repo := range []string{
		"https://github.com/acme/%zz", `https:/\CANARY@github.com/x`, `git@git\CANARY.example.com:acme/api`,
		"git@github.com:acme/CANARY\tapi", "https://github.com/acme/CANARY api", "git@github.com:acme/\u0085CANARY",
		"https:/CANARY@github.com/acme/api", "HTTPS://github.com/acme/CANARY",
	} {
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
	credGitHub      = "github"
	credAnthropic   = "anthropic"
	repoAPI         = "acme/api"
	repoWeb         = "acme/web"
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

func (s *PoolConfigSuite) TestNegativeZeroRefused() {
	_, err := Parse(append(s.readMinimal(), []byte("  capacity: {maxQueued: -0}\n")...))
	s.Require().ErrorContains(err, "spec.capacity.maxQueued (line 13)")
}

func (s *PoolConfigSuite) TestLoadRefusesLargeFile() {
	base := s.readMinimal()
	pad := func(size int) []byte {
		out := slices.Clone(base)
		out = append(out, '#')
		for len(out) < size-1 {
			out = append(out, 'x')
		}
		return append(out, '\n')
	}
	dir := s.T().TempDir()
	atCap, overCap := filepath.Join(dir, "at.yaml"), filepath.Join(dir, "over.yaml")
	s.Require().NoError(os.WriteFile(atCap, pad(maxFileSize), 0o600))
	s.Require().NoError(os.WriteFile(overCap, pad(maxFileSize+1), 0o600))
	_, err := Load(atCap)
	s.Require().NoError(err)
	_, err = Load(overCap)
	s.Require().ErrorIs(err, errFileTooLarge)
	_, err = Parse(pad(maxFileSize + 1))
	s.Require().ErrorIs(err, errFileTooLarge)
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

func (s *PoolConfigSuite) versions(p *Pool) (string, string) {
	policy, err := p.PolicyVersion()
	s.Require().NoError(err)
	config, err := p.ConfigVersion()
	s.Require().NoError(err)
	return policy, config
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
