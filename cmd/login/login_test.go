package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/suite"
)

const (
	testEmail    = "alice@example.test"
	testPassword = "correct-horse-battery-staple"
	testToken    = "sess_0123456789abcdef0123456789abcdef"
	testKeyID    = "AbCdEfGh12345678"
	testSecret   = "S3cretS3cretS3cretS3cretS3cretS3cretS3cr"
	testFullKey  = "dvsy_" + testKeyID + "_" + testSecret
	testKeyRecID = "7d9f4c1e-0000-4000-8000-000000000001"

	orgAcme    = "Acme Labs"
	orgGlobex  = "Globex"
	routeLogin = "POST /v1/auth/login"
	orgOne     = "org-1"
	orgTwo     = "org-2"
	roleOwner  = "owner"
	roleAdmin  = "admin"
)

var keyPattern = regexp.MustCompile(`dvsy_[A-Za-z0-9]+_[A-Za-z0-9_-]+`)

type org struct {
	ID, Name, Role string
}

type recordedCall struct {
	Method, Path, Auth string
	Body               map[string]any
}

// fakeGateway implements the gateway routes the login flow uses.
type fakeGateway struct {
	server *httptest.Server

	mu    sync.Mutex
	calls []recordedCall

	password      string
	orgs          []org
	createKeyCode int
}

func newFakeGateway() *fakeGateway {
	g := &fakeGateway{
		password:      testPassword,
		orgs:          []org{{ID: orgOne, Name: orgAcme, Role: roleOwner}},
		createKeyCode: http.StatusCreated,
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.handle))
	return g
}

func (g *fakeGateway) handle(w http.ResponseWriter, r *http.Request) {
	call := recordedCall{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&call.Body)
	}
	g.mu.Lock()
	g.calls = append(g.calls, call)
	g.mu.Unlock()

	switch call.Method + " " + call.Path {
	case routeLogin:
		g.login(w, call)
	case "GET /v1/orgs":
		g.listOrgs(w, call)
	case "POST /v1/auth/logout":
		w.WriteHeader(http.StatusNoContent)
	default:
		g.orgKeyRoute(w, call)
	}
}

func (g *fakeGateway) orgKeyRoute(w http.ResponseWriter, call recordedCall) {
	switch {
	case call.Method == http.MethodPost && strings.HasSuffix(call.Path, "/api-keys"):
		g.createKey(w, call)
	case call.Method == http.MethodDelete && strings.Contains(call.Path, "/api-keys/"):
		w.WriteHeader(http.StatusNoContent)
	default:
		writeEnvelope(w, http.StatusNotFound, "not_found", "no such route")
	}
}

func (g *fakeGateway) login(w http.ResponseWriter, call recordedCall) {
	if call.Body["email"] != testEmail || call.Body["password"] != g.password {
		writeEnvelope(
			w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": testToken})
}

func (g *fakeGateway) listOrgs(w http.ResponseWriter, call recordedCall) {
	if call.Auth != "Bearer "+testToken {
		writeEnvelope(w, http.StatusUnauthorized, "unauthorized", "missing session")
		return
	}
	out := make([]map[string]string, 0, len(g.orgs))
	for _, o := range g.orgs {
		out = append(out, map[string]string{"id": o.ID, "name": o.Name, "role": o.Role})
	}
	writeJSON(w, http.StatusOK, map[string]any{"orgs": out})
}

func (g *fakeGateway) createKey(w http.ResponseWriter, call recordedCall) {
	if g.createKeyCode != http.StatusCreated {
		writeEnvelope(w, g.createKeyCode, "forbidden", "owner or admin role required")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     testKeyRecID,
		"key_id": testKeyID,
		"name":   call.Body["name"],
		"scopes": call.Body["scopes"],
		"key":    testFullKey,
	})
}

func (g *fakeGateway) called(method, path string) []recordedCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []recordedCall
	for _, c := range g.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func (g *fakeGateway) sequence() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, 0, len(g.calls))
	for _, c := range g.calls {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeEnvelope(w http.ResponseWriter, status int, code, message string) {
	body := map[string]string{"code": code, "message": message}
	writeJSON(w, status, map[string]any{"error": body})
}

type LoginSuite struct {
	suite.Suite

	gateway *fakeGateway
	home    string
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	h       helpers
}

func TestLoginSuite(t *testing.T) {
	suite.Run(t, new(LoginSuite))
}

func (s *LoginSuite) SetupTest() {
	s.h = helpers{s: s}
	s.home = s.T().TempDir()
	s.T().Setenv(config.EnvHome, s.home)
	s.T().Setenv(config.EnvConfig, "")
	config.ResetPathManager()
	s.T().Cleanup(config.ResetPathManager)

	s.gateway = newFakeGateway()
	s.T().Cleanup(s.gateway.server.Close)
	s.out = &bytes.Buffer{}
	s.errOut = &bytes.Buffer{}
}

// helpers are kept off LoginSuite so the suite's exported test methods stay
// ahead of every unexported method.
type helpers struct{ s *LoginSuite }

func (h helpers) newCmd(in string) *Cmd {
	c := newCmd()
	c.In = strings.NewReader(in)
	c.Out = h.s.out
	c.ErrOut = h.s.errOut
	return c
}

func (h helpers) run(c *Cmd) error {
	return c.Run(context.Background(), h.s.gateway.server.URL)
}

func (h helpers) credentials() input {
	return input{email: testEmail, password: testPassword}
}

func (h helpers) configPath() string {
	path, err := config.GetConfigPath()
	h.s.Require().NoError(err)
	return path
}

func (h helpers) writeLooseConfig(content []byte) {
	//nolint:gosec // loose mode is the precondition under test
	err := os.WriteFile(h.configPath(), content, 0o644)
	h.s.Require().NoError(err)
}

func (h helpers) combinedOutput() string {
	return h.s.out.String() + h.s.errOut.String()
}

type input struct{ email, password string }

func (i input) String() string { return i.email + "\n" + i.password + "\n" }

func (s *LoginSuite) TestSuccessStoresKeyAndPrintsEmailAndOrg() {
	s.Require().NoError(s.h.run(s.h.newCmd(s.h.credentials().String())))

	stdout := s.out.String()
	s.Contains(stdout, testEmail)
	s.Contains(stdout, orgAcme)

	cfg, err := config.LoadConfig("", "")
	s.Require().NoError(err)
	s.Require().NotNil(cfg.Gateway)
	s.Equal(s.gateway.server.URL, cfg.Gateway.URL)
	s.Equal(orgOne, cfg.Gateway.OrgID)
	s.Equal(orgAcme, cfg.Gateway.OrgName)
	s.Equal(testKeyID, cfg.Gateway.KeyID)
	s.Equal(testKeyRecID, cfg.Gateway.KeyRecordID)
	s.Equal(testFullKey, cfg.Gateway.APIKey)
}

func (s *LoginSuite) TestCallsGatewayInOrderWithScopesAndSessionToken() {
	s.Require().NoError(s.h.run(s.h.newCmd(s.h.credentials().String())))

	s.Equal([]string{
		routeLogin,
		"GET /v1/orgs",
		"POST /v1/orgs/org-1/api-keys",
		"POST /v1/auth/logout",
	}, s.gateway.sequence())

	create := s.gateway.called("POST", "/v1/orgs/org-1/api-keys")
	s.Require().Len(create, 1)
	s.Equal("Bearer "+testToken, create[0].Auth)
	s.Equal("devsy-cli", create[0].Body["name"])
	s.Equal([]any{"read", "write"}, create[0].Body["scopes"])
}

func (s *LoginSuite) TestOutputNeverContainsKeyPasswordOrToken() {
	s.Require().NoError(s.h.run(s.h.newCmd(s.h.credentials().String())))

	all := s.h.combinedOutput()
	s.NotRegexp(keyPattern, all)
	s.NotContains(all, testSecret)
	s.NotContains(all, testPassword)
	s.NotContains(all, testToken)
}

func (s *LoginSuite) TestConfigFileModeIs0600() {
	s.Require().NoError(s.h.run(s.h.newCmd(s.h.credentials().String())))

	info, err := os.Stat(s.h.configPath())
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o600), info.Mode().Perm())
}

func (s *LoginSuite) TestExistingLooseConfigModeIsTightenedTo0600() {
	s.h.writeLooseConfig([]byte("defaultContext: default\n"))

	s.Require().NoError(s.h.run(s.h.newCmd(s.h.credentials().String())))

	info, err := os.Stat(s.h.configPath())
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o600), info.Mode().Perm())
	raw, err := os.ReadFile(s.h.configPath())
	s.Require().NoError(err)
	s.Contains(string(raw), "defaultContext: default")
}

func (s *LoginSuite) TestNothingUnderConfigDirHoldsPasswordOrSessionToken() {
	s.Require().NoError(s.h.run(s.h.newCmd(s.h.credentials().String())))

	err := filepath.WalkDir(s.home, func(path string, d os.DirEntry, walkErr error) error {
		s.Require().NoError(walkErr)
		if d.IsDir() {
			return nil
		}
		raw, readErr := readTestFile(path)
		if readErr != nil {
			return nil // lock files may be unreadable transient entries
		}
		s.NotContains(string(raw), testPassword, path)
		s.NotContains(string(raw), testToken, path)
		return nil
	})
	s.Require().NoError(err)
}

func (s *LoginSuite) TestWrongPasswordFailsAndLeavesConfigUntouched() {
	existing := []byte("defaultContext: default\n")
	s.h.writeLooseConfig(existing)
	before, err := os.Stat(s.h.configPath())
	s.Require().NoError(err)

	err = s.h.run(s.h.newCmd(input{testEmail, "wrong-password-123"}.String()))

	s.Require().Error(err)
	s.Contains(err.Error(), "invalid email or password")
	after, readErr := os.ReadFile(s.h.configPath())
	s.Require().NoError(readErr)
	s.Equal(existing, after)
	afterInfo, statErr := os.Stat(s.h.configPath())
	s.Require().NoError(statErr)
	s.Equal(before.Mode(), afterInfo.Mode())
	s.Equal(before.ModTime(), afterInfo.ModTime())
}

func (s *LoginSuite) TestWrongPasswordCreatesNoConfigAndNoAPIKey() {
	err := s.h.run(s.h.newCmd(input{testEmail, "wrong-password-123"}.String()))

	s.Require().Error(err)
	_, statErr := os.Stat(s.h.configPath())
	s.True(errors.Is(statErr, os.ErrNotExist), "config file must not be created")
	s.Equal([]string{routeLogin}, s.gateway.sequence())
	s.NotContains(s.h.combinedOutput(), "wrong-password-123")
}

func (s *LoginSuite) TestSingleOrgIsUsedWithoutPrompting() {
	s.Require().NoError(s.h.run(s.h.newCmd(s.h.credentials().String())))

	s.NotContains(s.errOut.String(), "Select")
}

func (s *LoginSuite) TestMultipleOrgsPromptsForChoice() {
	s.gateway.orgs = []org{
		{ID: orgOne, Name: orgAcme, Role: roleOwner},
		{ID: orgTwo, Name: orgGlobex, Role: roleAdmin},
	}

	s.Require().NoError(s.h.run(s.h.newCmd(s.h.credentials().String() + "2\n")))

	s.Contains(s.errOut.String(), orgAcme)
	s.Contains(s.errOut.String(), orgGlobex)
	s.Len(s.gateway.called("POST", "/v1/orgs/org-2/api-keys"), 1)
	s.Empty(s.gateway.called("POST", "/v1/orgs/org-1/api-keys"))
	s.Contains(s.out.String(), orgGlobex)
	cfg, err := config.LoadConfig("", "")
	s.Require().NoError(err)
	s.Equal(orgTwo, cfg.Gateway.OrgID)
	s.Equal(orgGlobex, cfg.Gateway.OrgName)
}

func (s *LoginSuite) TestInvalidOrgChoiceFailsWithoutKeyOrConfig() {
	s.gateway.orgs = []org{
		{ID: orgOne, Name: orgAcme, Role: roleOwner},
		{ID: orgTwo, Name: orgGlobex, Role: roleAdmin},
	}

	err := s.h.run(s.h.newCmd(s.h.credentials().String() + "9\n"))

	s.Require().Error(err)
	s.Empty(s.gateway.called("POST", "/v1/orgs/org-1/api-keys"))
	s.Empty(s.gateway.called("POST", "/v1/orgs/org-2/api-keys"))
	_, statErr := os.Stat(s.h.configPath())
	s.True(errors.Is(statErr, os.ErrNotExist))
}

func (s *LoginSuite) TestOrgFlagSelectsByNameOrID() {
	s.gateway.orgs = []org{
		{ID: orgOne, Name: orgAcme, Role: roleOwner},
		{ID: orgTwo, Name: orgGlobex, Role: roleAdmin},
	}

	for _, selector := range []string{orgGlobex, orgTwo} {
		c := s.h.newCmd(s.h.credentials().String())
		c.Org = selector
		s.Require().NoError(s.h.run(c), selector)
		cfg, err := config.LoadConfig("", "")
		s.Require().NoError(err)
		s.Equal(orgTwo, cfg.Gateway.OrgID, selector)
		s.NotContains(s.errOut.String(), "Select")
	}
}

func (s *LoginSuite) TestUnknownOrgFlagFailsWithoutKeyOrConfig() {
	c := s.h.newCmd(s.h.credentials().String())
	c.Org = "does-not-exist"

	err := s.h.run(c)

	s.Require().Error(err)
	s.Contains(err.Error(), "does-not-exist")
	s.Empty(s.gateway.called("POST", "/v1/orgs/org-1/api-keys"))
	_, statErr := os.Stat(s.h.configPath())
	s.True(errors.Is(statErr, os.ErrNotExist))
}

func (s *LoginSuite) TestNoOrgsFails() {
	s.gateway.orgs = nil

	err := s.h.run(s.h.newCmd(s.h.credentials().String()))

	s.Require().Error(err)
	s.Contains(err.Error(), "no organizations")
}

func (s *LoginSuite) TestKeyCreationRefusedLeavesConfigUntouched() {
	s.gateway.createKeyCode = http.StatusForbidden

	err := s.h.run(s.h.newCmd(s.h.credentials().String()))

	s.Require().Error(err)
	s.Contains(err.Error(), "owner or admin role required")
	_, statErr := os.Stat(s.h.configPath())
	s.True(errors.Is(statErr, os.ErrNotExist))
	s.NotContains(s.h.combinedOutput(), testToken)
}

func (s *LoginSuite) TestPasswordIsReadWithoutEchoFromTerminal() {
	c := s.h.newCmd(testEmail + "\n")
	c.stdinIsTerminal = true
	secretReads := 0
	c.readSecret = func() (string, error) {
		secretReads++
		return testPassword, nil
	}

	s.Require().NoError(s.h.run(c))

	s.Equal(1, secretReads)
	s.NotContains(s.h.combinedOutput(), testPassword)
	s.Contains(s.errOut.String(), "Password")
}

func (s *LoginSuite) TestEmptyEmailOrPasswordIsRejectedBeforeAnyRequest() {
	for _, in := range []string{"\n\n", testEmail + "\n\n"} {
		err := s.h.run(s.h.newCmd(in))
		s.Require().Error(err, in)
	}
	s.Empty(s.gateway.sequence())
}

func (s *LoginSuite) TestFailedConfigWriteRevokesIssuedKey() {
	blocker := filepath.Join(s.home, "blocker")
	s.Require().NoError(os.WriteFile(blocker, nil, 0o600))
	s.T().Setenv(config.EnvConfig, filepath.Join(blocker, "config.yaml"))

	err := s.h.run(s.h.newCmd(s.h.credentials().String()))

	s.Require().Error(err)
	s.Len(s.gateway.called("DELETE", "/v1/orgs/org-1/api-keys/"+testKeyRecID), 1)
	s.NotRegexp(keyPattern, err.Error())
	s.NotRegexp(keyPattern, s.h.combinedOutput())
}

func (s *LoginSuite) TestGatewayURLTrailingSlashIsTrimmed() {
	c := s.h.newCmd(s.h.credentials().String())

	s.Require().NoError(c.Run(context.Background(), s.gateway.server.URL+"/"))

	cfg, err := config.LoadConfig("", "")
	s.Require().NoError(err)
	s.Equal(s.gateway.server.URL, cfg.Gateway.URL)
}

func (s *LoginSuite) TestGatewayURLWithoutHTTPSchemeIsRejected() {
	for _, raw := range []string{"", "localhost:8180", "ftp://example.test", "http://"} {
		err := s.h.newCmd(s.h.credentials().String()).Run(context.Background(), raw)
		s.Require().Error(err, raw)
	}
	s.Empty(s.gateway.sequence())
}

func (s *LoginSuite) TestUnreachableGatewayFailsWithoutConfig() {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()

	err := s.h.newCmd(s.h.credentials().String()).Run(context.Background(), url)

	s.Require().Error(err)
	s.NotContains(err.Error(), testPassword)
	_, statErr := os.Stat(s.h.configPath())
	s.True(errors.Is(statErr, os.ErrNotExist))
}

func readTestFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // path comes from walking the test temp dir
}
