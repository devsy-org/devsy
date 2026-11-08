package workspace

import (
	"encoding/json"
	"net/http"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/spf13/cobra"
)

type lifecycleCommand struct {
	name    string
	method  string
	path    string
	build   func(*flags.GlobalFlags) *cobra.Command
	subject string
}

func lifecycleCommands() []lifecycleCommand {
	return []lifecycleCommand{
		{
			"sleep",
			http.MethodPost,
			"/v1/workspaces/" + platName + "/sleep",
			NewSleepCmd,
			"sleeping",
		},
		{"wake", http.MethodPost, "/v1/workspaces/" + platName + "/wake", NewWakeCmd, "waking"},
		{"delete", http.MethodDelete, "/v1/workspaces/" + platName, NewDeleteCmd, "deleting"},
	}
}

func (s *PlatformSuite) run(tc lifecycleCommand, args ...string) error {
	return s.execute(tc.build(&flags.GlobalFlags{ResultFormat: output.ModePlain}), args...)
}

func (s *PlatformSuite) TestLifecycleCommandsCallTheirRouteWithTheStoredKey() {
	for _, tc := range lifecycleCommands() {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.login()

			s.Require().NoError(s.run(tc, platName))

			calls := s.gateway.recorded()
			s.Require().Len(calls, 1, "the command must not poll the gateway")
			s.Equal(tc.method, calls[0].Method)
			s.Equal(tc.path, calls[0].Path)
			s.Equal("Bearer "+platTestKey, calls[0].Auth)
			s.Contains(s.out.String(), platName)
			s.NotContains(s.out.String(), platTestKey)
		})
	}
}

func (s *PlatformSuite) TestLifecycleCommandsPrintJSONWhenAsked() {
	s.login()

	cmd := NewSleepCmd(&flags.GlobalFlags{ResultFormat: output.ModeJSON})
	s.Require().NoError(s.execute(cmd, platName))

	var got map[string]string
	s.Require().NoError(json.Unmarshal(s.out.Bytes(), &got))
	s.Equal(platName, got[fieldName])
	s.Contains(got, fieldPhase)
}

func (s *PlatformSuite) TestLifecycleCommandsFailWithTheGatewayMessage() {
	for _, tc := range lifecycleCommands() {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.login()
			s.gateway.status = http.StatusNotFound
			s.gateway.message = "workspace not found"

			err := s.run(tc, platName)

			s.Require().Error(err)
			s.Contains(err.Error(), "workspace not found")
			s.Contains(err.Error(), tc.subject)
			s.Contains(err.Error(), platName)
			s.NotContains(err.Error(), platTestKey)
		})
	}
}

func (s *PlatformSuite) TestLifecycleCommandsShowThePermissionMessage() {
	for _, tc := range lifecycleCommands() {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.login()
			s.gateway.status = http.StatusForbidden
			s.gateway.message = "key lacks write scope"

			err := s.run(tc, platName)

			s.Require().Error(err)
			s.Contains(err.Error(), "permission denied: key lacks write scope")
		})
	}
}

func (s *PlatformSuite) TestLifecycleCommandsSuggestLoginOnUnauthorized() {
	for _, tc := range lifecycleCommands() {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.login()
			s.gateway.status = http.StatusUnauthorized

			err := s.run(tc, platName)

			s.Require().Error(err)
			s.Contains(err.Error(), "devsy login")
			s.NotContains(err.Error(), platTestKey)
		})
	}
}

func (s *PlatformSuite) TestSleepAndWakeWithoutLoginSuggestLogin() {
	for _, tc := range lifecycleCommands()[:2] {
		s.Run(tc.name, func() {
			s.SetupTest()

			err := s.run(tc, platName)

			s.Require().Error(err)
			s.Contains(err.Error(), "devsy login")
			s.Empty(s.gateway.recorded())
		})
	}
}

func (s *PlatformSuite) TestSleepAndWakeRequireExactlyOneName() {
	for _, tc := range lifecycleCommands()[:2] {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.login()

			s.Require().Error(s.run(tc))
			s.Require().Error(s.run(tc, "a", "b"))
			s.Empty(s.gateway.recorded())
		})
	}
}

func (s *PlatformSuite) TestDeleteSeveralNamesDeletesEachAndReportsEveryFailure() {
	s.login()

	s.Require().NoError(s.run(lifecycleCommands()[2], platWsA, "ws-b"))

	calls := s.gateway.recorded()
	s.Require().Len(calls, 2)
	s.Equal("/v1/workspaces/"+platWsA, calls[0].Path)
	s.Equal("/v1/workspaces/ws-b", calls[1].Path)
}

func (s *PlatformSuite) TestDeleteKeepsGoingAfterAFailureAndExitsNonZero() {
	s.login()
	s.gateway.status = http.StatusNotFound
	s.gateway.message = "workspace not found"

	err := s.run(lifecycleCommands()[2], platWsA, "ws-b")

	s.Require().Error(err)
	s.Len(s.gateway.recorded(), 2, "a failure must not stop the remaining deletes")
	s.Contains(err.Error(), platWsA)
	s.Contains(err.Error(), "ws-b")
	s.Contains(err.Error(), "workspace not found")
}

func (s *PlatformSuite) TestDeleteSkipPlatformLeavesTheGatewayAlone() {
	s.login()

	_ = s.run(lifecycleCommands()[2], "--skip-platform", "no-such-local-workspace")

	s.Empty(s.gateway.recorded())
}
