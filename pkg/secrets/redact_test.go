package secrets_test

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
)

const (
	redactTestSecret = "sk-live-abcdef123456"
	redactTestKey    = "DEVSY_API_KEY=" + redactTestSecret
)

func TestRedactor_MasksValues(t *testing.T) {
	r := secrets.NewRedactor([]string{
		"DB_PASSWORD=hunter2-correct-horse",
		"TOKEN=abc123def456",
	})

	got := r.Redact("connecting with hunter2-correct-horse and token abc123def456")
	want := "connecting with *** and token ***"
	if got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
}

func TestRedactor_SkipsTooShortValues(t *testing.T) {
	r := secrets.NewRedactor([]string{"DEVSY_AUTH_BOOTSTRAP=1"})

	got := r.Redact("version v1.2.3 in /var/run/001/mount")
	if want := "version v1.2.3 in /var/run/001/mount"; got != want {
		t.Errorf("Redact = %q, want short value left unmasked (%q)", got, want)
	}
	if got := r.SkippedValueCount(); got != 1 {
		t.Errorf("SkippedValueCount = %d, want 1", got)
	}
}

// Maskable values alongside a too-short one are still masked, and only the
// short value is reported as skipped.
func TestRedactor_MasksLongValuesAlongsideShortOnes(t *testing.T) {
	r := secrets.NewRedactor([]string{
		"DEVSY_AUTH_BOOTSTRAP=1",
		redactTestKey,
	})

	got := r.Redact("key=sk-live-abcdef123456 bootstrap=1")
	if want := "key=*** bootstrap=1"; got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
	if got := r.SkippedValueCount(); got != 1 {
		t.Errorf("SkippedValueCount = %d, want 1", got)
	}
}

func TestRedactor_SkippedValueCountIsZeroWhenAllMaskable(t *testing.T) {
	r := secrets.NewRedactor([]string{redactTestKey})
	if got := r.SkippedValueCount(); got != 0 {
		t.Errorf("SkippedValueCount = %d, want 0", got)
	}

	var nilRedactor *secrets.Redactor
	if got := nilRedactor.SkippedValueCount(); got != 0 {
		t.Errorf("nil SkippedValueCount = %d, want 0", got)
	}
}

// The environment redactor is the path that selects sensitive keys.
func TestEnvironmentRedactor_ShortSensitiveValueDoesNotCorruptOutput(t *testing.T) {
	r := secrets.NewEnvironmentRedactor([]string{
		"DEVSY_DEFERRED_AUTH_BOOTSTRAP=1",
		redactTestKey,
	})

	got := r.Redact("agent version v1.2.3 at /volumes/ws-001/_data key sk-live-abcdef123456")
	want := "agent version v1.2.3 at /volumes/ws-001/_data key ***"
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
		{"A=sec-abcd", "B=sec-abcd-efgh"},
		{"B=sec-abcd-efgh", "A=sec-abcd"},
	} {
		r := secrets.NewRedactor(order)
		got := r.Redact("value is sec-abcd-efgh here")
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
