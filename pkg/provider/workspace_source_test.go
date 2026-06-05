package provider

import "testing"

func TestParseWorkspaceSource_GitURLs(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantRepo  string
		wantValid bool
	}{
		{
			name:      "https",
			in:        "git:https://github.com/devsy-org/devsy",
			wantRepo:  "https://github.com/devsy-org/devsy",
			wantValid: true,
		},
		{
			name:      "ssh scheme",
			in:        "git:ssh://git@github.com/devsy-org/devsy",
			wantRepo:  "ssh://git@github.com/devsy-org/devsy",
			wantValid: true,
		},
		{
			name:      "scp-like",
			in:        "git:git@github.com:devsy-org/devsy.git",
			wantRepo:  "git@github.com:devsy-org/devsy.git",
			wantValid: true,
		},
		{
			name:      "bare host normalizes to https",
			in:        "git:github.com/devsy-org/devsy",
			wantRepo:  "https://github.com/devsy-org/devsy",
			wantValid: true,
		},
		{
			// The flaky CI signature: workspace_list output round-tripped back
			// to workspace_create. NormalizeRepository now strips the leading
			// "git:" so this no longer becomes "https://git:https://...".
			name:      "double git: prefix from workspace_list round-trip",
			in:        "git:git:https://github.com/devsy-org/devsy",
			wantRepo:  "https://github.com/devsy-org/devsy",
			wantValid: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := ParseWorkspaceSource(tc.in)
			if tc.wantValid && src == nil {
				t.Fatalf("ParseWorkspaceSource(%q) returned nil; want valid source", tc.in)
			}
			if !tc.wantValid && src != nil {
				t.Fatalf("ParseWorkspaceSource(%q) returned %+v; want nil", tc.in, src)
			}
			if src != nil && src.GitRepository != tc.wantRepo {
				t.Errorf("GitRepository = %q; want %q", src.GitRepository, tc.wantRepo)
			}
		})
	}
}

func TestIsPlausibleGitSource(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"https", "https://github.com/devsy-org/devsy", true},
		{"ssh scheme", "ssh://git@github.com/devsy-org/devsy", true},
		{"scp-like", "git@github.com:devsy-org/devsy.git", true},
		{"file", "file:///workspace/repo", true},
		{
			"nested scheme (the user-reported bug)",
			"https://git:https://github.com/devsy-org/devsy",
			false,
		},
		{"bare host (not normalized)", "github.com/devsy-org/devsy", false},
		{"unknown scheme", "ftp://example.com/repo", false},
		{"missing host", "https://", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPlausibleGitSource(tc.in); got != tc.want {
				t.Errorf("isPlausibleGitSource(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}
