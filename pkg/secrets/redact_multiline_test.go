package secrets_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/suite"
)

const numericFixture = "TOKEN={\n\"pin\": 1\n}"

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

func (s *multilineRedactionSuite) TestNumericJSONSecretIsMaskedWithoutBreakingJSON() {
	redactor := secrets.NewRedactor([]string{"TOKEN={\n\"pin\": 123456\n}"})
	output := redactor.Redact(`{"level":"info","pin": 123456}`)
	s.True(json.Valid([]byte(output)))
	s.NotContains(output, "123456")
	s.NotContains(redactor.Redact(`"pin": 123456`), "123456")
}

func (s *multilineRedactionSuite) TestScalarValuesRemainMaskedAfterCombining() {
	values := []struct{ name, value, line, hidden string }{
		{
			name:   "array number",
			value:  "[\n  123456\n]",
			line:   `{"values":[123456]}`,
			hidden: "123456",
		},
		{
			name:   "boolean",
			value:  "{\n\"enabled\": true\n}",
			line:   `{"enabled": true}`,
			hidden: "true",
		},
		{
			name:   "nested",
			value:  "{\n\"inner\": {\n\"pin\": 123456\n}\n}",
			line:   `{"pin": 123456}`,
			hidden: "123456",
		},
	}
	for _, tc := range values {
		s.Run(tc.name, func() {
			redactor := secrets.Combine(
				secrets.NewRedactor([]string{"TOKEN=" + tc.value}),
				secrets.NewRedactor(nil),
			)
			output := redactor.Redact(tc.line)
			s.True(json.Valid([]byte(output)))
			s.NotContains(output, tc.hidden)
			if tc.name == "array number" {
				s.NotContains(redactor.Redact("  123456,"), tc.hidden)
			}
		})
	}
}

func (s *multilineRedactionSuite) TestNumericSecretDoesNotChangeUnrelatedJSON() {
	redactor := secrets.NewRedactor([]string{numericFixture})
	input := `{"schemaVersion":1,"message":"ordinary"}`
	s.Equal(input, redactor.Redact(input))
}

func (s *multilineRedactionSuite) TestScalarSecretDoesNotPartiallyReplaceOtherNumbers() {
	redactor := secrets.NewRedactor([]string{numericFixture})
	input := `{"pin":123,"schemaVersion":1}`
	output := redactor.Redact(input)
	s.Equal(input, output)
	s.True(json.Valid([]byte(output)))
}

func (s *multilineRedactionSuite) TestNumericSecretDoesNotChangeSplitJSONWrites() {
	redactor := secrets.NewStreamingRedactor(
		secrets.NewRedactor([]string{numericFixture}),
	)
	output := redactor.RedactChunk(
		`{"schemaVersion":`,
	) + redactor.RedactChunk(
		"1",
	) + redactor.RedactChunk(
		"}",
	) + redactor.Flush()
	s.Equal(`{"schemaVersion":1}`, output)
}

func (s *multilineRedactionSuite) TestLargeJSONNumberIsMasked() {
	redactor := secrets.NewRedactor([]string{"TOKEN={\n\"amount\": 1e10000\n}"})
	output := redactor.Redact(`{"amount": 1e10000}`)
	s.True(json.Valid([]byte(output)))
	s.NotContains(output, "1e10000")
}

func (s *multilineRedactionSuite) TestNumericStringSecretDoesNotCorruptUnrelatedJSON() {
	redactor := secrets.NewRedactor([]string{"TOKEN={\n\"pin\": \"1\"\n}"})
	input := `{"schemaVersion":1,"message":"ordinary"}`
	s.Equal(input, redactor.Redact(input))
	output := redactor.Redact(`{"message":"1"}`)
	s.True(json.Valid([]byte(output)))
	s.NotContains(output, `"1"`)
}

func (s *multilineRedactionSuite) TestNumericSecretIsMaskedInTextAndJSONString() {
	redactor := secrets.NewRedactor([]string{"TOKEN={\n\"pin\": 123456\n}"})
	s.NotContains(redactor.Redact("agent reported 123456"), "123456")
	output := redactor.Redact(`{"schemaVersion":1,"message":"agent reported 123456"}`)
	s.True(json.Valid([]byte(output)))
	s.NotContains(output, "123456")
	s.Contains(output, `"schemaVersion":1`)
}

func (s *multilineRedactionSuite) TestStreamingNumericStringSecretsAreMasked() {
	for _, chunks := range [][]string{
		{"PIN 123456\n"},
		{"PIN 123", "456\n"},
		{`{"message":"PIN 123456","schemaVersion":1}`, "\n"},
	} {
		redactor := secrets.NewStreamingRedactor(
			secrets.NewRedactor([]string{"TOKEN={\n\"pin\":\"123456\"\n}"}),
		)
		var output string
		for _, chunk := range chunks {
			output += redactor.RedactChunk(chunk)
		}
		output += redactor.Flush()
		s.NotContains(output, "123456")
	}
}

func (s *multilineRedactionSuite) TestStreamingOversizedLineIsBoundedAndMasked() {
	redactor := secrets.NewStreamingRedactor(
		secrets.NewRedactor([]string{"TOKEN={\n\"pin\":\"123456\"\n}"}),
	)
	output := redactor.RedactChunk(strings.Repeat("a", 1<<20))
	output += redactor.RedactChunk("123456\nnext line\n")
	output += redactor.Flush()
	s.Equal("***\nnext line\n", output)
}
