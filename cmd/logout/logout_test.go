package logout

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/suite"
)

const (
	testKey      = "test-api-key-placeholder"
	testKeyID    = "AbCdEfGh12345678"
	testKeyRecID = "7d9f4c1e-0000-4000-8000-000000000001"
	testOrgID    = "org-1"
	testOrgName  = "Acme Labs"

	revokePath = "/v1/orgs/org-1/api-keys/" + testKeyRecID
)

var keyPattern = regexp.MustCompile(`dvsy_[A-Za-z0-9]+_[A-Za-z0-9_-]+`)

type recordedCall struct {
	Method, Path, Auth string
}

// fakeGateway answers the revoke route with a configurable status.
type fakeGateway struct {
	server *httptest.Server

	mu     sync.Mutex
	calls  []recordedCall
	status int
}

func newFakeGateway() *fakeGateway {
	g := &fakeGateway{status: http.StatusNoContent}
	g.server = httptest.NewServer(http.HandlerFunc(g.handle))
	return g
}

func (g *fakeGateway) handle(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.calls = append(g.calls, recordedCall{
		Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"),
	})
	status := g.status
	g.mu.Unlock()

	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": "error", "message": "gateway says no"},
	})
}

func (g *fakeGateway) recorded() []recordedCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]recordedCall(nil), g.calls...)
}

type LogoutSuite struct {
	suite.Suite

	gateway *fakeGateway
	out     *bytes.Buffer
	errOut  *bytes.Buffer
}

func TestLogoutSuite(t *testing.T) {
	suite.Run(t, new(LogoutSuite))
}

func (s *LogoutSuite) SetupTest() {
	s.T().Setenv(config.EnvHome, s.T().TempDir())
	s.T().Setenv(config.EnvConfig, "")
	config.ResetPathManager()
	s.T().Cleanup(config.ResetPathManager)

	s.gateway = newFakeGateway()
	s.T().Cleanup(s.gateway.server.Close)
	s.out = &bytes.Buffer{}
	s.errOut = &bytes.Buffer{}
}

func (s *LogoutSuite) TestSuccessRevokesWithTheStoredKeyAndClearsTheLogin() {
	s.login(s.gateway.server.URL)

	s.Require().NoError(s.run())

	calls := s.gateway.recorded()
	s.Require().Len(calls, 1)
	s.Equal(http.MethodDelete, calls[0].Method)
	s.Equal(revokePath, calls[0].Path)
	s.Equal("Bearer "+testKey, calls[0].Auth)
	s.assertLoggedOut()
}

func (s *LogoutSuite) TestAlreadyRevokedKeyStillClearsTheLogin() {
	s.login(s.gateway.server.URL)
	s.gateway.status = http.StatusNotFound

	s.Require().NoError(s.run())

	s.Len(s.gateway.recorded(), 1)
	s.assertLoggedOut()
}

func (s *LogoutSuite) TestOtherGatewayErrorsFailAndKeepTheKey() {
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		s.Run(http.StatusText(status), func() {
			s.login(s.gateway.server.URL)
			s.gateway.status = status
			before := s.configBytes()

			err := s.run()

			s.Require().Error(err)
			s.Contains(err.Error(), "revoking")
			s.NotContains(err.Error(), testKey)
			s.Equal(before, s.configBytes(), "a failed logout must not touch the config")
		})
	}
}

func (s *LogoutSuite) TestUnreachableGatewayFailsAndKeepsTheKey() {
	s.login(s.gateway.server.URL)
	s.gateway.server.Close()
	before := s.configBytes()

	err := s.run()

	s.Require().Error(err)
	s.NotContains(err.Error(), testKey)
	s.Equal(before, s.configBytes())
}

func (s *LogoutSuite) TestKeyWithoutRecordIDCannotBeRevokedAndIsKept() {
	s.Require().NoError(config.UpdateConfig("", "", func(cfg *config.Config) error {
		cfg.Gateway = &config.GatewayCredentials{
			URL: s.gateway.server.URL, OrgID: testOrgID, APIKey: testKey,
		}
		return nil
	}))
	before := s.configBytes()

	err := s.run()

	s.Require().Error(err)
	s.Empty(s.gateway.recorded(), "nothing can be revoked without a key record ID")
	s.Equal(before, s.configBytes())
}

func (s *LogoutSuite) TestWithoutALoginExitsCleanlyWithoutCallingTheGateway() {
	s.Require().NoError(s.run())

	s.Empty(s.gateway.recorded())
	s.Contains(s.out.String(), "Not logged in")
}

func (s *LogoutSuite) TestLoggingOutTwiceIsHarmless() {
	s.login(s.gateway.server.URL)
	s.Require().NoError(s.run())
	s.out.Reset()

	s.Require().NoError(s.run())

	s.Len(s.gateway.recorded(), 1, "the second logout has no key to revoke")
	s.Contains(s.out.String(), "Not logged in")
	s.assertLoggedOut()
}

func (s *LogoutSuite) TestKeepsTheGatewayURLAsALoggedOutMarker() {
	s.login(s.gateway.server.URL)

	s.Require().NoError(s.run())

	cfg, err := config.LoadConfig("", "")
	s.Require().NoError(err)
	s.Require().NotNil(cfg.Gateway)
	s.Equal(s.gateway.server.URL, cfg.Gateway.URL)
}

func (s *LogoutSuite) TestLeavesANewerLoginUntouched() {
	s.login(s.gateway.server.URL)
	s.gateway.status = http.StatusNoContent
	s.Require().NoError(config.UpdateConfig("", "", func(cfg *config.Config) error {
		cfg.Gateway.KeyRecordID = "rec-newer"
		cfg.Gateway.APIKey = "newer-api-key-placeholder"
		return nil
	}))

	stale := &config.GatewayCredentials{
		URL: s.gateway.server.URL, OrgID: testOrgID, KeyRecordID: testKeyRecID, APIKey: testKey,
	}
	s.Require().NoError(clearLogin(stale))

	cfg, err := config.LoadConfig("", "")
	s.Require().NoError(err)
	s.Equal("rec-newer", cfg.Gateway.KeyRecordID)
	s.Equal("newer-api-key-placeholder", cfg.Gateway.APIKey)
}

func (s *LogoutSuite) TestNeverPrintsTheKey() {
	s.login(s.gateway.server.URL)
	s.Require().NoError(s.run())

	combined := s.out.String() + s.errOut.String()
	s.NotContains(combined, testKey)
	s.False(keyPattern.MatchString(combined))
	s.Contains(combined, "Logged out")
}

func (s *LogoutSuite) TestRejectsArguments() {
	s.login(s.gateway.server.URL)
	cmd := NewLogoutCmd()
	cmd.SetOut(s.out)
	cmd.SetErr(s.errOut)
	cmd.SetArgs([]string{"extra"})

	s.Require().Error(cmd.ExecuteContext(context.Background()))
	s.Empty(s.gateway.recorded())
}

func (s *LogoutSuite) login(url string) {
	s.Require().NoError(config.UpdateConfig("", "", func(cfg *config.Config) error {
		cfg.Gateway = &config.GatewayCredentials{
			URL:         url,
			OrgID:       testOrgID,
			OrgName:     testOrgName,
			KeyID:       testKeyID,
			KeyRecordID: testKeyRecID,
			APIKey:      testKey,
		}
		return nil
	}))
}

func (s *LogoutSuite) run() error {
	cmd := NewLogoutCmd()
	cmd.SetOut(s.out)
	cmd.SetErr(s.errOut)
	cmd.SetArgs(nil)
	return cmd.ExecuteContext(context.Background())
}

func (s *LogoutSuite) configBytes() []byte {
	path, err := config.GetConfigPath()
	s.Require().NoError(err)
	//nolint:gosec // path comes from config.GetConfigPath in a temp home
	data, err := os.ReadFile(path)
	s.Require().NoError(err)
	return data
}

func (s *LogoutSuite) assertLoggedOut() {
	cfg, err := config.LoadConfig("", "")
	s.Require().NoError(err)
	if cfg.Gateway != nil {
		s.Empty(cfg.Gateway.APIKey)
		s.Empty(cfg.Gateway.KeyID)
		s.Empty(cfg.Gateway.KeyRecordID)
		s.Empty(cfg.Gateway.OrgID)
		s.Empty(cfg.Gateway.OrgName)
	}
	s.NotContains(string(s.configBytes()), testKey)
}
