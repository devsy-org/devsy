package download

import (
	"testing"
)

const testBaseURL = "https://example.com/file.tgz"

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "no userinfo", raw: testBaseURL, want: testBaseURL},
		// #nosec G101 -- test credential
		{
			name: "with userinfo",
			raw:  "https://user:xxxxx@example.com/file.tgz",
			want: testBaseURL,
		},
		{name: "user only", raw: "https://user@example.com/path", want: "https://example.com/path"},
		{name: "no scheme", raw: "example.com/file", want: "example.com/file"},
		{name: "empty", raw: "", want: ""},
		// #nosec G101 -- test credential
		{
			name: "malformed with userinfo",
			raw:  "https://user:p%ZZ@host/path",
			want: "https://host/path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeURL(tt.raw)
			if got != tt.want {
				t.Errorf("sanitizeURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
