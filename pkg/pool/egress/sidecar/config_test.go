package sidecar

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/stretchr/testify/suite"
)

const (
	testSession = "p-test-pool-abcdefghij"
	keyDefault  = "default"
	keyAllow    = "allow"
	testPoolNet = "172.30.0.0/16"
	valDeny     = "deny"
	keyPorts    = "ports"
	keyHost     = "host"
	hostA       = "a.example.com"
)

type ConfigSuite struct {
	suite.Suite
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigSuite))
}

func absPath(parts ...string) string {
	return string(filepath.Separator) + filepath.Join(parts...)
}

func (s *ConfigSuite) TestAValidConfigParses() {
	cfg, err := s.parse(s.valid())
	s.Require().NoError(err)
	s.Equal(testSession, cfg.SessionID)
	s.Equal("172.30.0.2:3128", cfg.Bind)
	s.Len(cfg.Egress.Allow, 1)
	s.Equal([]string{testPoolNet}, cfg.AlwaysDeny)
}

func (s *ConfigSuite) TestAWildcardOrMissingBindIsRefused() {
	for _, bind := range []string{"", "0.0.0.0:3128", "[::]:3128", ":3128", "localhost:3128", "172.30.0.2"} {
		doc := s.valid()
		doc["bind"] = bind
		_, err := s.parse(doc)
		s.Error(err, "bind %q", bind)
	}
}

func (s *ConfigSuite) TestBadValuesAreRefused() {
	cases := map[string]func(map[string]any){
		"version":        func(d map[string]any) { d["version"] = 2 },
		"no version":     func(d map[string]any) { delete(d, "version") },
		"session":        func(d map[string]any) { d["sessionId"] = "Bad Session" },
		"relative sock":  func(d map[string]any) { d["eventsSocket"] = "events/s.sock" },
		"no sock":        func(d map[string]any) { delete(d, "eventsSocket") },
		"default allow":  func(d map[string]any) { d["egress"] = map[string]any{keyDefault: "allow"} },
		"bad rule":       func(d map[string]any) { d["egress"] = map[string]any{keyAllow: []any{map[string]any{}}} },
		"bad deny":       func(d map[string]any) { d["alwaysDeny"] = []string{"172.30.0.0"} },
		"mapped deny":    func(d map[string]any) { d["alwaysDeny"] = []string{"::ffff:10.0.0.0/104"} },
		"negative":       func(d map[string]any) { d["maxConns"] = -1 },
		"huge conns":     func(d map[string]any) { d["maxConns"] = 1 << 30 },
		"negative idle":  func(d map[string]any) { d["idleSeconds"] = -1 },
		"huge idle":      func(d map[string]any) { d["idleSeconds"] = maxSeconds + 1 },
		"huge lifetime":  func(d map[string]any) { d["lifetimeSeconds"] = maxSeconds + 1 },
		"no deny list":   func(d map[string]any) { delete(d, "alwaysDeny") },
		"empty deny":     func(d map[string]any) { d["alwaysDeny"] = []string{} },
		"unknown field":  func(d map[string]any) { d["extra"] = true },
		"unknown nested": func(d map[string]any) { d["egress"] = map[string]any{keyDefault: valDeny, "x": 1} },
	}
	for name, mutate := range cases {
		doc := s.valid()
		mutate(doc)
		_, err := s.parse(doc)
		s.Error(err, name)
	}
}

func (s *ConfigSuite) TestEgressRulesFollowThePoolConfigRules() {
	rule := func(key string, value any) map[string]any {
		return map[string]any{key: value, keyPorts: []int{443}}
	}
	cases := map[string][]any{
		"noncanonical cidr": {rule("cidr", "20.1.2.3/16")},
		"reserved cidr":     {rule("cidr", "169.254.169.254/32")},
		"loopback cidr":     {rule("cidr", "127.0.0.0/8")},
		"too wide cidr":     {rule("cidr", "20.0.0.0/4")},
		"duplicate rule": {
			rule(keyHost, hostA), rule(keyHost, hostA),
		},
		"duplicate cidr spelling": {
			rule("cidr", "20.0.0.0/16"), rule("cidr", "20.0.0.0/16"),
		},
		"duplicate port": {
			map[string]any{keyHost: hostA, keyPorts: []int{443, 443}},
		},
		"port range":    {map[string]any{keyHost: hostA, keyPorts: []int{70000}}},
		"host and cidr": {map[string]any{keyHost: hostA, "cidr": "20.0.0.0/16"}},
		"internal host": {rule(keyHost, "db.internal")},
		"uppercase":     {rule(keyHost, "A.example.com")},
		"ip as host":    {rule(keyHost, "20.1.1.1")},
	}
	for name, allow := range cases {
		doc := s.valid()
		doc["egress"] = map[string]any{keyDefault: valDeny, keyAllow: allow}
		_, err := s.parse(doc)
		s.Error(err, name)
	}
}

func (s *ConfigSuite) TestAMissingDefaultIsRefused() {
	doc := s.valid()
	doc["egress"] = map[string]any{}
	_, err := s.parse(doc)
	s.ErrorContains(err, valDeny)
}

func (s *ConfigSuite) TestProxyLimitsReachTheProxyConfig() {
	cfg := Config{
		SessionID: testSession, Bind: "172.30.0.2:3128",
		MaxConns: 7, IdleSeconds: 11, LifetimeSeconds: 13,
	}
	got := proxyConfig(cfg)
	s.Equal(7, got.MaxConns)
	s.Equal(11*time.Second, got.IdleTimeout)
	s.Equal(13*time.Second, got.MaxLifetime)
	s.Equal(testSession, got.SessionID)
	s.Equal(cfg.Bind, got.Bind)

	zero := proxyConfig(Config{})
	s.Zero(zero.MaxConns)
	s.Zero(zero.IdleTimeout)
	s.Zero(zero.MaxLifetime)
}

func (s *ConfigSuite) TestAlwaysDenyReachesThePolicy() {
	doc := s.valid()
	doc["egress"] = map[string]any{
		keyDefault: valDeny,
		keyAllow:   []any{map[string]any{"cidr": "10.0.0.0/8", keyPorts: []int{443}}},
	}
	doc["alwaysDeny"] = []string{"10.1.0.0/16"}
	cfg, err := s.parse(doc)
	s.Require().NoError(err)
	policy, err := cfg.policy()
	s.Require().NoError(err)

	s.Equal(egress.PoolNetwork, policy.CheckAddr(netip.MustParseAddr("10.1.2.3"), 443).Reason)
	s.True(policy.CheckAddr(netip.MustParseAddr("10.9.9.9"), 443).Allow)
}

func (s *ConfigSuite) TestTheRefusalNamesTheCause() {
	doc := s.valid()
	doc["bind"] = "0.0.0.0:3128"
	_, err := s.parse(doc)
	s.ErrorContains(err, "bind")

	doc = s.valid()
	delete(doc, "alwaysDeny")
	_, err = s.parse(doc)
	s.ErrorContains(err, "alwaysDeny")
}

func (s *ConfigSuite) TestTrailingDataAndBadJSONAreRefused() {
	raw, err := json.Marshal(s.valid())
	s.Require().NoError(err)
	for name, data := range map[string][]byte{
		"two objects": append(append([]byte{}, raw...), raw...),
		"trailing":    append(append([]byte{}, raw...), []byte(" x")...),
		"truncated":   raw[:len(raw)/2],
		"empty":       nil,
		"array":       []byte("[]"),
	} {
		_, err := Parse(data)
		s.Error(err, name)
	}
}

func (s *ConfigSuite) TestAnOversizedConfigIsRefusedWithoutBeingParsed() {
	doc := s.valid()
	doc["sessionId"] = strings.Repeat("a", maxConfigBytes)
	raw, err := json.Marshal(doc)
	s.Require().NoError(err)
	_, err = Parse(raw)
	s.ErrorContains(err, "larger than")
}

func (s *ConfigSuite) TestLoadReadsAFileAndRefusesAMissingOrOversizedOne() {
	dir := s.T().TempDir()
	raw, err := json.Marshal(s.valid())
	s.Require().NoError(err)
	path := filepath.Join(dir, "egress.json")
	s.Require().NoError(os.WriteFile(path, raw, 0o600))
	cfg, err := Load(path)
	s.Require().NoError(err)
	s.Equal(testSession, cfg.SessionID)

	_, err = Load(filepath.Join(dir, "missing.json"))
	s.Error(err)

	big := filepath.Join(dir, "big.json")
	s.Require().NoError(os.WriteFile(big, []byte(strings.Repeat(" ", maxConfigBytes+1)), 0o600))
	_, err = Load(big)
	s.ErrorContains(err, "larger than")
}

func (s *ConfigSuite) valid() map[string]any {
	return map[string]any{
		"version":      1,
		"sessionId":    testSession,
		"bind":         "172.30.0.2:3128",
		"eventsSocket": absPath("events", "s.sock"),
		"egress": map[string]any{
			keyDefault: valDeny,
			keyAllow:   []any{map[string]any{keyHost: "*.example.com", keyPorts: []int{443}}},
		},
		"alwaysDeny": []string{testPoolNet},
	}
}

func (s *ConfigSuite) parse(doc map[string]any) (Config, error) {
	raw, err := json.Marshal(doc)
	s.Require().NoError(err)
	return Parse(raw)
}
