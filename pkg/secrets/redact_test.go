package secrets_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/suite"
)

func TestRedactor_MasksValues(t *testing.T) {
	r := secrets.NewRedactor([]string{"DB_PASSWORD=hunter2", "TOKEN=abc123"})

	got := r.Redact("connecting with hunter2 and token abc123")
	want := "connecting with *** and token ***"
	if got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
}

func TestRedactor_IgnoresEmptyValues(t *testing.T) {
	r := secrets.NewRedactor([]string{"EMPTY=", "NO_EQUALS"})

	got := r.Redact("nothing to mask here")
	if got != "nothing to mask here" {
		t.Errorf("Redact = %q, want unchanged", got)
	}
}

func TestRedactor_NilAndEmpty(t *testing.T) {
	var r *secrets.Redactor
	if got := r.Redact("passthrough"); got != "passthrough" {
		t.Errorf("nil Redactor changed input: %q", got)
	}

	empty := secrets.NewRedactor(nil)
	if got := empty.Redact("passthrough"); got != "passthrough" {
		t.Errorf("empty Redactor changed input: %q", got)
	}
}

func TestRedactor_ValueWithEquals(t *testing.T) {
	r := secrets.NewRedactor([]string{"CONN=user=admin;pw=secret"})

	got := r.Redact("dsn: user=admin;pw=secret end")
	if got != "dsn: *** end" {
		t.Errorf("Redact = %q, want masked full value", got)
	}
}

// A shorter value that is a prefix of a longer one must not partially mask the
// longer value, regardless of input order.
func TestRedactor_PrefixOverlap(t *testing.T) {
	for _, order := range [][]string{
		{"A=sec", "B=secret"},
		{"B=secret", "A=sec"},
	} {
		r := secrets.NewRedactor(order)
		got := r.Redact("value is secret here")
		if got != "value is *** here" {
			t.Errorf("order %v: Redact = %q, want longer value fully masked", order, got)
		}
	}
}

func TestEnvironmentRedactorOnlyMasksSensitiveKeys(t *testing.T) {
	r := secrets.NewEnvironmentRedactor([]string{
		"PATH=/usr/local/bin",
		"DEVSY_TOKEN=DEVSY_SECRET_TEST_846297",
	})
	got := r.Redact("path=/usr/local/bin token=DEVSY_SECRET_TEST_846297")
	want := "path=/usr/local/bin token=***"
	if got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
}

func TestCombineMasksValuesFromEveryRedactor(t *testing.T) {
	r := secrets.Combine(
		secrets.NewRedactor([]string{"A=alpha-secret"}),
		secrets.NewRedactor([]string{"B=beta-secret"}),
	)
	if got := r.Redact("alpha-secret beta-secret"); got != "*** ***" {
		t.Errorf("combined redaction = %q, want %q", got, "*** ***")
	}
}

func TestRedactor_MasksCredentialBearingURLsAndAuthorizationHeaders(t *testing.T) {
	//nolint:gosec // redaction fixture intentionally contains credential-shaped text
	input := "clone https://git-user:git-token@example.com/repo " +
		"and Authorization: Bearer api-token"
	got := secrets.NewRedactor(nil).Redact(input)
	if strings.Contains(got, "git-token") || strings.Contains(got, "api-token") {
		t.Fatalf("credential escaped format redaction: %q", got)
	}
	if !strings.Contains(got, "https://***@example.com/repo") ||
		!strings.Contains(got, "Authorization: Bearer ***") {
		t.Fatalf("format redaction = %q", got)
	}
}

func TestStreamingRedactor_MasksFormatCredentialsWithoutKnownValues(t *testing.T) {
	const expectedURL = "https://***@example.com"
	r := secrets.NewStreamingRedactor(secrets.NewRedactor(nil))
	got := r.RedactChunk("https://user:pass@example.com") + r.Flush()
	if got != expectedURL {
		t.Fatalf("streaming format redaction = %q", got)
	}
}

func TestStreamingRedactor_MasksSplitCredentialURLWithoutKnownValues(t *testing.T) {
	r := secrets.NewStreamingRedactor(secrets.NewRedactor(nil))
	first := r.RedactChunk("https://user:pass")
	second := r.RedactChunk("@example.com")
	got := first + second + r.Flush()
	if strings.Contains(got, "pass") || got != "https://***@example.com" {
		t.Fatalf("split streaming URL redaction = %q", got)
	}
}

func TestStreamingRedactor_MasksCredentialsSplitInsideFormatPrefix(t *testing.T) {
	tests := []struct {
		name   string
		first  string
		second string
		want   string
	}{
		{
			name:   "url scheme",
			first:  "https:/",
			second: "/user:pass@example.com",
			want:   "https://***@example.com",
		},
		{
			name:   "authorization header",
			first:  "Authorization: Bearer ",
			second: "api-token",
			want:   "Authorization: Bearer ***",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := secrets.NewStreamingRedactor(secrets.NewRedactor(nil))
			got := r.RedactChunk(tt.first) + r.RedactChunk(tt.second) + r.Flush()
			if got != tt.want {
				t.Fatalf("streaming redaction = %q, want %q", got, tt.want)
			}
		})
	}
}

type LogEscapeSuite struct{ suite.Suite }

func TestLogEscapeSuite(t *testing.T) { suite.Run(t, new(LogEscapeSuite)) }

func (s *LogEscapeSuite) TestNilAndEmptyKeepFormatMasking() {
	for _, base := range []*secrets.Redactor{nil, secrets.NewRedactor(nil)} {
		r := secrets.WithLogEscapes(base)
		s.Equal("plain", r.Redact("plain"))
		s.Equal("https://***@host", r.Redact("https://user:pw@host"))
	}
}

func (s *LogEscapeSuite) TestMasksRawQuotedJSONAndTextForms() {
	const value = "a\nb\r\"c\"\\d\x00\x0b\x0c\u2028"
	r := secrets.WithLogEscapes(secrets.NewRedactor([]string{"TOKEN=" + value}))

	quoted := strings.Trim(strconv.Quote(value), `"`)
	encoded, err := json.Marshal(value)
	s.Require().NoError(err)
	jsonBody := strings.Trim(string(encoded), `"`)
	zapBody := `a\nb\r\"c\"\\d\u0000\u000b\u000c` + "\u2028"
	textBody := `a\nb\r"c"\d\x00\v\f\u2028`
	textFieldBody := `a\nb\r\"c\"\\d\u0000\u000b\u000c\u2028`

	for name, form := range map[string]string{
		"raw": value, "strconv": quoted, "json": jsonBody, "zapJSON": zapBody, "text": textBody,
		"textField": textFieldBody,
	} {
		s.Equal("[***]", r.Redact("["+form+"]"), name)
	}
}

func (s *LogEscapeSuite) TestDoesNotMutateInputAndKeepsRawValues() {
	original := secrets.NewRedactor([]string{"TOKEN=a\nb"})
	derived := secrets.WithLogEscapes(original)

	s.Equal("***", derived.Redact("a\nb"))
	s.Equal(`a\nb`, original.Redact(`a\nb`), "input redactor must not learn escaped forms")
	s.Equal("***", original.Redact("a\nb"))
}

func (s *LogEscapeSuite) TestSafeValueHasSingleVariantAndOrdinaryTextUntouched() {
	r := secrets.WithLogEscapes(secrets.NewRedactor([]string{"TOKEN=alnum42"}))

	s.Equal("*** and other-value", r.Redact("alnum42 and other-value"))
}

func (s *LogEscapeSuite) TestValueWithEqualsSurvives() {
	r := secrets.WithLogEscapes(secrets.NewRedactor([]string{"CONN=user=admin;pw=a\nb"}))

	s.Equal("[***] [***]", r.Redact("[user=admin;pw=a\nb] [user=admin;pw=a\\nb]"))
}

func (s *LogEscapeSuite) TestLogEscapeVariantsPreserveLongestFirst() {
	r := secrets.WithLogEscapes(secrets.NewRedactor([]string{"A=sec", "B=sec\nret"}))

	input := "x sec\nret " + `sec\nret` + " sec y"
	s.Equal("x *** *** *** y", r.Redact(input))
}

func (s *LogEscapeSuite) TestSplitWritesNeverLeakSerializedVariants() {
	r := secrets.WithLogEscapes(secrets.NewRedactor([]string{"A=sec", "B=sec\nret\"x"}))
	for _, input := range []string{
		"ordinary " + `sec\nret\"x` + " tail",
		"ordinary sec\nret\"x tail",
		"ordinary " + `sec\nret"x` + " sec",
	} {
		want := r.Redact(input)
		s.NotContains(want, "ret")
		for split := 0; split <= len(input); split++ {
			stream := secrets.NewStreamingRedactor(r)
			got := stream.RedactChunk(input[:split]) + stream.RedactChunk(input[split:]) +
				stream.Flush()
			s.Equal(want, got, "input %q split %d", input, split)
		}
	}
}
