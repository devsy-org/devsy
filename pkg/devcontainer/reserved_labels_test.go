package devcontainer

import (
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

type ReservedIdentitySuite struct{ suite.Suite }

func TestReservedIdentitySuite(t *testing.T) { suite.Run(t, new(ReservedIdentitySuite)) }

func (s *ReservedIdentitySuite) TestPersistedLabelsRejectedBeforeDriverAccess() {
	r := &runner{workspaceConfig: &provider.AgentWorkspaceInfo{}}
	r.workspaceConfig.CLIOptions.IDLabels = []string{"devsy.user=fixture-private-value"}
	result, err := r.Up(s.T().Context(), UpOptions{}, time.Second, nil)
	s.ErrorContains(err, "devsy.user")
	s.NotContains(err.Error(), "fixture-private-value")
	s.Nil(result)
}
