package download

import (
	"testing"
)

const testBaseURL = "https://example.com/file.tgz"

const (
	testGithubOrg  = "devsy-org"
	testGithubRepo = "devsy"
	testGithubFile = "devsy-linux-amd64"
	testGithubTag  = "v1.2.3"
)

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

func TestParseGithubURLValid(t *testing.T) {
	tests := []struct {
		name string
		path string
		want githubReleaseRef
	}{
		{
			name: "latest release",
			path: "/" + testGithubOrg + "/" + testGithubRepo + "/releases/latest/download/" + testGithubFile,
			want: githubReleaseRef{
				org:  testGithubOrg,
				repo: testGithubRepo,
				file: testGithubFile,
			},
		},
		{
			name: "tagged release",
			path: "/" + testGithubOrg + "/" + testGithubRepo + "/releases/download/" + testGithubTag + "/" + testGithubFile,
			want: githubReleaseRef{
				org:     testGithubOrg,
				repo:    testGithubRepo,
				release: testGithubTag,
				file:    testGithubFile,
			},
		},
		{
			name: "latest without leading slash",
			path: testGithubOrg + "/" + testGithubRepo + "/releases/latest/download/" + testGithubFile,
			want: githubReleaseRef{
				org:  testGithubOrg,
				repo: testGithubRepo,
				file: testGithubFile,
			},
		},
		{
			name: "tagged without leading slash",
			path: testGithubOrg + "/" + testGithubRepo + "/releases/download/" + testGithubTag + "/" + testGithubFile,
			want: githubReleaseRef{
				org:     testGithubOrg,
				repo:    testGithubRepo,
				release: testGithubTag,
				file:    testGithubFile,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGithubURL(tt.path)
			if !ok {
				t.Fatalf("parseGithubURL(%q) ok = false, want true", tt.path)
			}
			if got != tt.want {
				t.Errorf("parseGithubURL(%q) = %+v, want %+v", tt.path, got, tt.want)
			}
		})
	}
}

func TestParseGithubURLInvalid(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "too few segments", path: "/devsy-org/devsy/releases/latest/download"},
		{name: "too many segments", path: "/devsy-org/devsy/releases/latest/download/dir/file"},
		{name: "missing releases segment", path: "/devsy-org/devsy/archive/v1.2.3/file"},
		{name: "unknown middle segment", path: "/devsy-org/devsy/releases/foo/download/file"},
		{name: "latest without download", path: "/devsy-org/devsy/releases/latest/file"},
		{name: "empty path", path: ""},
		{name: "root slash", path: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := parseGithubURL(tt.path); ok {
				t.Errorf("parseGithubURL(%q) ok = true, want false", tt.path)
			}
		})
	}
}
