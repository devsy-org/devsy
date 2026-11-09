package up

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

type agentSecretRedactionSuite struct{ suite.Suite }

func TestAgentSecretRedactionSuite(t *testing.T) { suite.Run(t, new(agentSecretRedactionSuite)) }
func (s *agentSecretRedactionSuite) TestResolvedSecretsAreRedacted() {
	cmd := &UpCmd{CLIOptions: provider.CLIOptions{
		SecretsEnv:   []string{"ENV=private-env-value"},
		SecretsMount: []string{"MOUNT=private-mount-value"},
		BuildSecrets: []string{"BUILD=private-build-value"},
		GitToken:     &provider.GitToken{Token: "private-git-value"},
	}}
	redactor := workspaceSecretRedactor(cmd)
	values := []string{
		"private-env-value", "private-mount-value", "private-build-value", "private-git-value",
	}
	for _, value := range values {
		s.Run(value, func() { s.NotContains(redactor.Redact("agent: "+value), value) })
	}
}
