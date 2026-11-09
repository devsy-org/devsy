package devcontainer

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

type setupSecretRedactionSuite struct{ suite.Suite }

func TestSetupSecretRedactionSuite(t *testing.T) { suite.Run(t, new(setupSecretRedactionSuite)) }
func (s *setupSecretRedactionSuite) TestResolvedSecretsAreRedacted() {
	options := provider.CLIOptions{
		BuildSecrets: []string{"BUILD=private-build-value"},
		GitToken:     &provider.GitToken{Token: "private-git-value"},
	}
	redactor := setupSecretRedactor(
		[]string{"ENV=private-env-value"},
		[]string{"MOUNT=private-mount-value"},
		options,
	)
	values := []string{
		"private-env-value", "private-mount-value", "private-build-value", "private-git-value",
	}
	for _, value := range values {
		s.Run(value, func() { s.NotContains(redactor.Redact("agent: "+value), value) })
	}
}
