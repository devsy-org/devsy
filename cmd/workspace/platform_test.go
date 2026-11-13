package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/suite"
)

const (
	platTestKey  = "test-api-key-placeholder"
	platTemplate = "smoke-template"
	platName     = "ws-one"
	platWsA      = "ws-a"
	urlX         = "http://x"

	msgNotFound = "workspace not found"
	msgScope    = "insufficient scope"

	fieldName     = "name"
	fieldPhase    = "phase"
	phaseRunning  = "Running"
	phasePending  = "Pending"
	phaseSleeping = "Sleeping"
)

type platformCall struct {
	Method, Path, Auth string
	Body               map[string]string
}

type fakePlatformGateway struct {
	server *httptest.Server

	mu         sync.Mutex
	calls      []platformCall
	workspaces []map[string]string
	status     int
	message    string
	// notFound lists workspace names whose lifecycle routes answer 404.
	notFound map[string]bool
}

// lifecycleRoute recognises the sleep, wake and delete routes and returns the
// workspace name with the phase the real gateway answers with.
func lifecycleRoute(call platformCall) (name, phase string, ok bool) {
	rest, found := strings.CutPrefix(call.Path, "/v1/workspaces/")
	if !found {
		return "", "", false
	}
	switch {
	case call.Method == http.MethodDelete && !strings.Contains(rest, "/"):
		return rest, "Deleting", true
	case call.Method == http.MethodPost && strings.HasSuffix(rest, "/sleep"):
		return strings.TrimSuffix(rest, "/sleep"), phaseRunning, true
	case call.Method == http.MethodPost && strings.HasSuffix(rest, "/wake"):
		return strings.TrimSuffix(rest, "/wake"), phaseSleeping, true
	}
	return "", "", false
}

func newFakePlatformGateway() *fakePlatformGateway {
	g := &fakePlatformGateway{}
	g.server = httptest.NewServer(http.HandlerFunc(g.handle))
	return g
}

func writeGatewayError(w http.ResponseWriter, status int, message string) {
	if message == "" {
		message = "invalid api key"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": "error", "message": message},
	})
}

// failureFor returns the status and message the gateway answers with instead
// of success, or status 0. The caller holds g.mu.
func (g *fakePlatformGateway) failureFor(call platformCall) (int, string) {
	if g.status != 0 {
		return g.status, g.message
	}
	if name, _, ok := lifecycleRoute(call); ok && g.notFound[name] {
		return http.StatusNotFound, msgNotFound
	}
	return 0, ""
}

func (g *fakePlatformGateway) handle(w http.ResponseWriter, r *http.Request) {
	call := platformCall{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&call.Body)
	}
	g.mu.Lock()
	g.calls = append(g.calls, call)
	status, message := g.failureFor(call)
	g.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		writeGatewayError(w, status, message)
		return
	}
	if name, phase, ok := lifecycleRoute(call); ok {
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{fieldName: name, fieldPhase: phase})
		return
	}
	switch call.Method + " " + call.Path {
	case "GET /v1/workspaces":
		_ = json.NewEncoder(w).Encode(g.workspaces)
	case "POST /v1/workspaces":
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{
			fieldName:  call.Body[fieldName],
			fieldPhase: phasePending,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (g *fakePlatformGateway) recorded() []platformCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]platformCall(nil), g.calls...)
}

type PlatformSuite struct {
	suite.Suite

	gateway *fakePlatformGateway
	out     *bytes.Buffer
}

func TestPlatformSuite(t *testing.T) {
	suite.Run(t, new(PlatformSuite))
}

func (s *PlatformSuite) SetupTest() {
	s.T().Setenv(config.EnvHome, s.T().TempDir())
	s.T().Setenv(config.EnvConfig, "")
	s.T().Setenv("DEVSY_UI", "")
	config.ResetPathManager()
	s.T().Cleanup(config.ResetPathManager)

	s.gateway = newFakePlatformGateway()
	s.T().Cleanup(s.gateway.server.Close)
	s.out = &bytes.Buffer{}
}

func (s *PlatformSuite) TestListPrintsOneRowPerWorkspaceWithNameAndPhase() {
	s.login()
	s.gateway.workspaces = []map[string]string{
		{fieldName: platWsA, fieldPhase: phaseRunning},
		{fieldName: "ws-b", fieldPhase: phaseSleeping},
		{fieldName: "ws-c", fieldPhase: "Failed"},
	}

	s.Require().NoError(s.list(output.ModePlain))

	lines := strings.Split(strings.TrimSpace(s.out.String()), "\n")
	s.Require().Len(lines, 4, s.out.String())
	s.Equal([]string{"NAME", "PHASE"}, strings.Fields(lines[0]))
	s.Equal([]string{platWsA, phaseRunning}, strings.Fields(lines[1]))
	s.Equal([]string{"ws-b", phaseSleeping}, strings.Fields(lines[2]))
	s.Equal([]string{"ws-c", "Failed"}, strings.Fields(lines[3]))
}

func (s *PlatformSuite) TestListSendsTheStoredKey() {
	s.login()

	s.Require().NoError(s.list(output.ModePlain))

	calls := s.gateway.recorded()
	s.Require().Len(calls, 1)
	s.Equal("GET", calls[0].Method)
	s.Equal("/v1/workspaces", calls[0].Path)
	s.Equal("Bearer "+platTestKey, calls[0].Auth)
	s.NotContains(s.out.String(), platTestKey)
}

func (s *PlatformSuite) TestListWithNoWorkspacesPrintsOnlyTheHeader() {
	s.login()

	s.Require().NoError(s.list(output.ModePlain))

	s.Equal([]string{"NAME", "PHASE"}, strings.Fields(s.out.String()))
}

func (s *PlatformSuite) TestListJSONHasNameAndPhaseOnly() {
	s.login()
	s.gateway.workspaces = []map[string]string{{fieldName: platWsA, fieldPhase: phaseRunning}}

	s.Require().NoError(s.list(output.ModeJSON))

	var rows []map[string]string
	s.Require().NoError(json.Unmarshal(s.out.Bytes(), &rows))
	s.Equal([]map[string]string{{fieldName: platWsA, fieldPhase: phaseRunning}}, rows)
}

func (s *PlatformSuite) TestListUnauthorizedSuggestsLogin() {
	s.login()
	s.gateway.status = http.StatusUnauthorized

	err := s.list(output.ModePlain)

	s.Require().Error(err)
	s.Contains(err.Error(), "devsy login")
	s.Contains(err.Error(), "listing workspaces")
	s.NotContains(err.Error(), platTestKey)
}

func (s *PlatformSuite) TestCreatePostsNameAndTemplateAndDoesNotPoll() {
	s.login()

	s.Require().NoError(s.create(platName, "--template", platTemplate))

	calls := s.gateway.recorded()
	s.Require().Len(calls, 1, "create must not poll the gateway")
	s.Equal("POST", calls[0].Method)
	s.Equal("/v1/workspaces", calls[0].Path)
	s.Equal("Bearer "+platTestKey, calls[0].Auth)
	s.Equal(map[string]string{fieldName: platName, "template": platTemplate}, calls[0].Body)
	s.Contains(s.out.String(), platName)
	s.NotContains(s.out.String(), platTestKey)
}

func (s *PlatformSuite) TestCreateRequiresTemplate() {
	s.login()

	err := s.create(platName)

	s.Require().Error(err)
	s.Contains(err.Error(), "template")
	s.Empty(s.gateway.recorded())
}

func (s *PlatformSuite) TestCreateRequiresExactlyOneName() {
	s.login()

	s.Require().Error(s.create("--template", platTemplate))
	s.Require().Error(s.create("a", "b", "--template", platTemplate))
	s.Empty(s.gateway.recorded())
}

func (s *PlatformSuite) TestCreateWithoutLoginSuggestsLogin() {
	err := s.create(platName, "--template", platTemplate)

	s.Require().Error(err)
	s.Contains(err.Error(), "devsy login")
	s.Empty(s.gateway.recorded())
}

func (s *PlatformSuite) TestCreateUnauthorizedSuggestsLogin() {
	s.login()
	s.gateway.status = http.StatusUnauthorized

	err := s.create(platName, "--template", platTemplate)

	s.Require().Error(err)
	s.Contains(err.Error(), "devsy login")
	s.Contains(err.Error(), "creating workspace")
	s.NotContains(err.Error(), platTestKey)
}

func (s *PlatformSuite) TestCreateForbiddenStatesTheKeyLacksWriteScope() {
	s.login()
	s.gateway.status = http.StatusForbidden
	s.gateway.message = msgScope

	err := s.create(platName, "--template", platTemplate)

	s.Require().Error(err)
	s.Contains(err.Error(), "permission denied")
	s.Contains(err.Error(), "stored API key lacks write scope")
	s.Contains(err.Error(), "creating workspace")
	s.NotContains(err.Error(), platTestKey)
	s.NotContains(s.out.String(), "accepted", "a refused create prints no success line")
}

func (s *PlatformSuite) TestPlatformCredentialsOnlyForACompleteLogin() {
	s.Nil(platformCredentials(nil))
	s.Nil(platformCredentials(&config.Config{}))
	s.Nil(platformCredentials(&config.Config{Gateway: &config.GatewayCredentials{URL: urlX}}))
	s.Nil(platformCredentials(&config.Config{Gateway: &config.GatewayCredentials{APIKey: "k"}}))

	creds := &config.GatewayCredentials{URL: urlX, APIKey: "k"}
	s.Same(creds, platformCredentials(&config.Config{Gateway: creds}))
}

func (s *PlatformSuite) TestListAfterLogoutFailsWithAnAuthenticationError() {
	s.loggedOut()

	err := s.list(output.ModePlain)

	s.Require().Error(err)
	s.Contains(err.Error(), "authentication")
	s.Contains(err.Error(), "devsy login")
	s.Empty(s.gateway.recorded(), "a logged-out list must not call the gateway")
}

func (s *PlatformSuite) TestListAfterLogoutWithSkipPlatformStaysLocal() {
	s.loggedOut()

	s.Require().NoError(s.execute(
		NewListCmd(&flags.GlobalFlags{ResultFormat: output.ModeJSON}), "--skip-platform",
	))
	s.Empty(s.gateway.recorded())
}

func (s *PlatformSuite) TestListWithoutAnyGatewayConfigStaysLocal() {
	s.Require().NoError(s.list(output.ModeJSON))
	s.Empty(s.gateway.recorded())
}

func (s *PlatformSuite) TestCreateAfterLogoutSuggestsLogin() {
	s.loggedOut()

	err := s.create(platName, "--template", platTemplate)

	s.Require().Error(err)
	s.Contains(err.Error(), "devsy login")
	s.Empty(s.gateway.recorded())
}

func (s *PlatformSuite) TestLoggedOutIsAGatewayURLWithoutAKey() {
	s.False(loggedOut(nil))
	s.False(loggedOut(&config.Config{}))
	s.False(loggedOut(&config.Config{Gateway: &config.GatewayCredentials{APIKey: "k"}}))
	s.False(loggedOut(&config.Config{
		Gateway: &config.GatewayCredentials{URL: urlX, APIKey: "k"},
	}))
	s.True(loggedOut(&config.Config{Gateway: &config.GatewayCredentials{URL: urlX}}))
}

func (s *PlatformSuite) loggedOut() {
	s.Require().NoError(config.UpdateConfig("", "", func(cfg *config.Config) error {
		cfg.Gateway = &config.GatewayCredentials{URL: s.gateway.server.URL}
		return nil
	}))
}

func (s *PlatformSuite) login() {
	s.Require().NoError(config.UpdateConfig("", "", func(cfg *config.Config) error {
		cfg.Gateway = &config.GatewayCredentials{
			URL:         s.gateway.server.URL,
			OrgID:       "org-1",
			OrgName:     "Acme",
			KeyID:       "AbCdEfGh12345678",
			KeyRecordID: "rec-1",
			APIKey:      platTestKey,
		}
		return nil
	}))
}

func (s *PlatformSuite) execute(cmd *cobra.Command, args ...string) error {
	cmd.SetOut(s.out)
	cmd.SetErr(s.out)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}

func (s *PlatformSuite) list(format string) error {
	return s.execute(NewListCmd(&flags.GlobalFlags{ResultFormat: format}))
}

func (s *PlatformSuite) create(args ...string) error {
	return s.execute(NewCreateCmd(&flags.GlobalFlags{ResultFormat: output.ModePlain}), args...)
}
