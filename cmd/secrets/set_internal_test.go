package secrets

import (
	"os"
	"strings"
	"testing"
)

func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		_, _ = w.WriteString(input)
		_ = w.Close()
	}()

	fn()
	_ = r.Close()
}

func TestResolveValue_Stdin(t *testing.T) {
	cmd := &SetCmd{Stdin: true}
	var got string
	var gotErr error
	withStdin(t, "s3cr3t\n", func() {
		got, gotErr = cmd.resolveValue("TOKEN")
	})
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	if got != "s3cr3t" {
		t.Fatalf("resolveValue(--stdin) = %q, want %q", got, "s3cr3t")
	}
}

func TestResolveValue_StdinPreservesInnerNewlines(t *testing.T) {
	cmd := &SetCmd{Stdin: true}
	pem := "-----BEGIN KEY-----\nline1\nline2\n-----END KEY-----"
	var got string
	withStdin(t, pem, func() {
		got, _ = cmd.resolveValue("TLS_KEY")
	})
	if got != pem {
		t.Fatalf("resolveValue(--stdin) = %q, want multiline value preserved", got)
	}
}

func TestResolveValue_StdinTrimsSingleTrailingNewlineOnly(t *testing.T) {
	cmd := &SetCmd{Stdin: true}
	var got string
	withStdin(t, "value\n\n", func() {
		got, _ = cmd.resolveValue("K")
	})
	if got != "value\n" {
		t.Fatalf("resolveValue = %q, want one trailing newline preserved", got)
	}
}

func TestResolveValue_Value(t *testing.T) {
	cmd := &SetCmd{Value: "plain", valueSet: true}
	got, err := cmd.resolveValue("K")
	if err != nil {
		t.Fatal(err)
	}
	if got != "plain" {
		t.Fatalf("resolveValue(--value) = %q, want %q", got, "plain")
	}
}

func TestResolveValue_ExplicitEmptyValue(t *testing.T) {
	// --value "" must be honored, not fall through to the interactive prompt.
	cmd := &SetCmd{Value: "", valueSet: true}
	got, err := cmd.resolveValue("K")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("resolveValue(--value \"\") = %q, want empty", got)
	}
}

func TestResolveValue_RejectsMultipleSources(t *testing.T) {
	cmd := &SetCmd{Value: "a", valueSet: true, Stdin: true}
	_, err := cmd.resolveValue("K")
	if err == nil || !strings.Contains(err.Error(), "only one of") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}
