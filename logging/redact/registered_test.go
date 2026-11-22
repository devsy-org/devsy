package redact

import "testing"

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
			want:   "x *** y",
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
