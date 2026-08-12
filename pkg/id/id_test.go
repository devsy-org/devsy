package id

import (
	"strings"
	"testing"
)

const (
	fooVal = "foo"
	barVal = "bar"

)

func TestSafeConcatName(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"single", []string{fooVal}, fooVal},
		{"multiple", []string{fooVal, barVal, "baz"}, "foo-bar-baz"},
		{"empty parts joined", []string{"", barVal}, "-bar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeConcatName(tc.in...); got != tc.want {
				t.Fatalf("SafeConcatName(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSafeConcatNameMaxUnderLimit(t *testing.T) {
	got := SafeConcatNameMax([]string{fooVal, barVal}, 63)
	if got != "foo-bar" {
		t.Fatalf("SafeConcatNameMax = %q, want %q", got, "foo-bar")
	}
}

func TestSafeConcatNameMaxExactLimit(t *testing.T) {
	input := strings.Repeat("a", 63)
	got := SafeConcatNameMax([]string{input}, 63)
	if len(got) != 63 {
		t.Fatalf("length = %d, want 63 (got %q)", len(got), got)
	}
	if got != input {
		t.Fatalf("SafeConcatNameMax at limit = %q, want %q", got, input)
	}
}

func TestSafeConcatNameMaxOverLimit(t *testing.T) {
	const maxLen = 63
	input := strings.Repeat("a", 100)
	got := SafeConcatNameMax([]string{input}, maxLen)
	if len(got) != maxLen {
		t.Fatalf("length = %d, want %d (got %q)", len(got), maxLen, got)
	}
	if got[maxLen-8] != '-' {
		t.Fatalf("expected hash separator at index %d, got %q", maxLen-8, got)
	}
	if got[:maxLen-8] != input[:maxLen-8] {
		t.Fatalf("prefix not preserved: %q", got)
	}
}

func TestSafeConcatNameMaxStripsTrailingDot(t *testing.T) {
	const maxLen = 63
	input := "a." + strings.Repeat("b", maxLen)
	got := SafeConcatNameMax([]string{input}, maxLen)
	if len(got) != maxLen {
		t.Fatalf("length = %d, want %d (got %q)", len(got), maxLen, got)
	}
	if strings.Contains(got, ".-") {
		t.Fatalf("expected trailing dot stripped, got %q", got)
	}
}

func TestSafeConcatNameMaxIsDeterministic(t *testing.T) {
	input := strings.Repeat("x", 100)
	first := SafeConcatNameMax([]string{input}, 63)
	second := SafeConcatNameMax([]string{input}, 63)
	if first != second {
		t.Fatalf("not deterministic: %q != %q", first, second)
	}
}

func TestToDockerImageName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "MyImage", "myimage"},
		{"strips invalid chars", "my.image:tag@special!", "myimagetagspecial"},
		{"keeps allowed separators", "my_image-name", "my_image-name"},
		{"empty stays empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToDockerImageName(tc.in); got != tc.want {
				t.Fatalf("ToDockerImageName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestToDockerImageNameOnlyLowercase(t *testing.T) {
	got := ToDockerImageName("ABC123")
	for _, r := range got {
		if r >= 'A' && r <= 'Z' {
			t.Fatalf("result contains uppercase rune %q in %q", r, got)
		}
	}
}
