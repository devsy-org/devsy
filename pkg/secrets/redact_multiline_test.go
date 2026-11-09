package secrets_test

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/suite"
)

type multilineRedactionSuite struct{ suite.Suite }

func TestMultilineRedactionSuite(t *testing.T) { suite.Run(t, new(multilineRedactionSuite)) }
func (s *multilineRedactionSuite) TestMultilineSecretsKeepUnrelatedJSONValid() {
	values := []string{
		"{\n\"password\": \"private-value-123\",\n\"punctuation\": \"{\"\n}",
		"{\nprivate-value-123\n}",
	}
	for _, value := range values {
		s.Run(value, func() {
			redactor := secrets.NewRedactor([]string{"TOKEN=" + value})
			output := redactor.Redact(`{"level":"info","message":"ordinary event"}`)
			s.True(json.Valid([]byte(output)))
			s.NotContains(redactor.Redact("agent: private-value-123"), "private-value-123")
		})
	}
}
