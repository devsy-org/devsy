package redact

import (
	"runtime"
	"strings"
	"testing"
)

const maskedMiddle = "x *** y"

func TestRedactRegisteredMasksOnlyRegisteredValues(t *testing.T) {
	multiline := "{\"Cookie\":\"x-secret\"}\nsecret-tail"
	escapedHeader := "\"\\\"Authorization: Bearer x\\\"\""
	tests := []struct {
		name, input, want string
		values            []string
	}{
		{
			name:   "multi-record value next to another registered value",
			values: []string{"other-secret", multiline},
			input:  "public head\nother-secret\n" + multiline + "\npublic tail",
			want:   "public head\n***\n***\npublic tail",
		},
		{
			name:   "carriage return separated value",
			values: []string{"other-secret", multiline},
			input:  "head\rother-secret\r" + "{\"Cookie\":\"x-secret\"}\nsecret-tail" + "\rtail",
			want:   "head\r***\r***\rtail",
		},
		{
			name:   "value containing an escaped header string",
			values: []string{escapedHeader},
			input:  escapedHeader + "\n secret-tail",
			want:   "***\n secret-tail",
		},
		{
			name:   "json escaped form of a registered value",
			values: []string{"a\"b"},
			input:  "{\"k\":\"a\\\"b\"} a\"b",
			want:   "{\"k\":\"***\"} ***",
		},
		{
			name:   "text patterns are not applied",
			values: []string{"registered-value"},
			input:  testCredentialURL() + " Authorization: Bearer abc",
			want:   testCredentialURL() + " Authorization: Bearer abc",
		},
		{
			name:   "overlapping values share one mask",
			values: []string{"abcd", "cdef"},
			input:  "x abcdef y",
			want:   maskedMiddle,
		},
		{
			name:   "no registered values",
			values: nil,
			input:  "plain text",
			want:   "plain text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(Options{Values: tt.values}).RedactRegistered(tt.input); got != tt.want {
				t.Fatalf("RedactRegistered(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
	var nilRedactor *Redactor
	if got := nilRedactor.RedactRegistered("value"); got != "value" {
		t.Fatalf("nil redactor changed the value to %q", got)
	}
}

func TestRedactRegisteredEncodedAndSeparatorForms(t *testing.T) {
	htmlForm := "<a&b>\"c"
	tests := []struct {
		name, input, want string
		options           Options
	}{
		{
			name:    "carriage return inside a value",
			options: Options{Values: []string{"first\rsecond"}},
			input:   "x first\rsecond y",
			want:    maskedMiddle,
		},
		{
			name:    "carriage return line feed inside a value",
			options: Options{Values: []string{"first\r\nsecond"}},
			input:   "x first\r\nsecond y",
			want:    maskedMiddle,
		},
		{
			name:    "encoder form with html escaping",
			options: Options{Values: []string{htmlForm}},
			input:   `{"k":"\u003ca\u0026b\u003e\"c"}`,
			want:    `{"k":"***"}`,
		},
		{
			name:    "encoder form without html escaping",
			options: Options{Values: []string{htmlForm}},
			input:   `{"k":"<a&b>\"c"}`,
			want:    `{"k":"***"}`,
		},
		{
			name:    "go quoted form",
			options: Options{Values: []string{"tab\there"}},
			input:   `fmt: "tab\there" raw: tab` + "\t" + `here`,
			want:    `fmt: "***" raw: ***`,
		},
		{
			name:    "sensitive environment entry",
			options: Options{Environment: []string{"SERVICE_TOKEN=env-secret", "PLAIN=env-public"}},
			input:   "env-secret env-public",
			want:    "*** env-public",
		},
		{
			name:    "short explicit values follow the minimum",
			options: Options{Values: []string{"ab", "abcd"}, MinExplicitBytes: 3},
			input:   "ab abcd",
			want:    "ab ***",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(tt.options).RedactRegistered(tt.input); got != tt.want {
				t.Fatalf("RedactRegistered(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func allocatedBytes(run func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestRedactRegisteredAllocatesInProportionToMatches(t *testing.T) {
	redactor := New(Options{Values: []string{"raw", "x\ty"}})
	large := strings.Repeat("z", 8<<20)
	var unchanged string
	if bytes := allocatedBytes(
		func() { unchanged = redactor.RedactRegistered(large) },
	); bytes > 1<<20 {
		t.Fatalf("no-match input allocated %d bytes", bytes)
	}
	if unchanged != large {
		t.Fatal("no-match input changed")
	}
	tail := large + "raw x\ty"
	var masked string
	if bytes := allocatedBytes(
		func() { masked = redactor.RedactRegistered(tail) },
	); bytes > 24<<20 {
		t.Fatalf("one-match input allocated %d bytes", bytes)
	}
	if want := large + "*** ***"; masked != want {
		t.Fatal("trailing matches were not masked")
	}
	blank := strings.Repeat("\n", 100_000) + "tail"
	if got := redactor.RedactRegistered(blank); got != blank {
		t.Fatal("blank records changed")
	}
}

func TestRedactRegisteredDenseInputsStayBounded(t *testing.T) {
	size := 8 << 20
	tests := []struct {
		name   string
		values []string
		input  string
		want   string
	}{
		{"single byte value", []string{"a"}, strings.Repeat("a", size), "***"},
		{"nested values", []string{"a", "aa", "aaa"}, strings.Repeat("a", size), "***"},
		{"backslash value", []string{"\\"}, strings.Repeat("\\", size), "***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redactor := New(Options{Values: tt.values})
			var got string
			if bytes := allocatedBytes(
				func() { got = redactor.RedactRegistered(tt.input) },
			); bytes > 1<<20 {
				t.Fatalf("allocated %d bytes", bytes)
			}
			if got != tt.want {
				t.Fatalf("dense input produced %d bytes", len(got))
			}
		})
	}
}

func TestRedactRegisteredRawAndEscapedFormsShareOneMask(t *testing.T) {
	got := New(Options{Values: []string{"\""}}).RedactRegistered(`x \" y`)
	if got != maskedMiddle {
		t.Fatalf("got %q", got)
	}
}

func TestRedactRegisteredLongSpanAbsorbsEarlierGroups(t *testing.T) {
	got := New(Options{Values: []string{"b", "xxabcdexx", "d"}}).RedactRegistered("1 xxabcdexx 2")
	if got != "1 *** 2" {
		t.Fatalf("got %q", got)
	}
	got = New(Options{Values: []string{"zzzzzz", "yy", "xx"}}).RedactRegistered("xx zzzzzz yy")
	if got != "*** *** ***" {
		t.Fatalf("got %q", got)
	}
}
