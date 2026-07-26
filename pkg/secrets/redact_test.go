package secrets_test

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
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
