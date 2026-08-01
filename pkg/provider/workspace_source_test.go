package provider

import "testing"

const (
	testRepoURL         = "https://github.com/devsy-org/devsy"
	testSchemeHTTPS     = "https"
	testProviderSnapRef = "ghcr.io/acme/s:my-ws-20260731150405"
)

func TestParseWorkspaceSource_GitURLs(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantRepo  string
		wantValid bool
	}{
		{
			name:      testSchemeHTTPS,
			in:        "git:" + testRepoURL,
			wantRepo:  testRepoURL,
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
			wantRepo:  testRepoURL,
			wantValid: true,
		},
		{
			// The flaky CI signature: workspace_list output round-tripped back
			// to workspace_create. NormalizeRepository now strips the leading
			// "git:" so this no longer becomes "https://git:https://...".
			name:      "double git: prefix from workspace_list round-trip",
			in:        "git:git:" + testRepoURL,
			wantRepo:  testRepoURL,
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
		{testSchemeHTTPS, testRepoURL, true},
		{"ssh scheme", "ssh://git@github.com/devsy-org/devsy", true},
		{"scp-like", "git@github.com:devsy-org/devsy.git", true},
		{"file", "file:///workspace/repo", true},
		{
			"nested scheme (the user-reported bug)",
			"https://git:" + testRepoURL,
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

func TestParseWorkspaceSource_Snapshot(t *testing.T) {
	src := ParseWorkspaceSource("snapshot:ghcr.io/acme/snapshots:my-ws-20260731150405")
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	if src.Snapshot != "ghcr.io/acme/snapshots:my-ws-20260731150405" {
		t.Fatalf("got snapshot ref %q", src.Snapshot)
	}
	if src.Type() != WorkspaceSourceSnapshot {
		t.Fatalf("got type %q, want %q", src.Type(), WorkspaceSourceSnapshot)
	}
	if src.String() != WorkspaceSourceSnapshot+"ghcr.io/acme/snapshots:my-ws-20260731150405" {
		t.Fatalf("got string %q", src.String())
	}
}

// TestWorkspaceSource_TypeAndStringAgreeOnKindPrecedence guards against a
// regression to two independently-maintained field-precedence switches:
// String() and Type() must always select the same populated field, even when
// a caller (incorrectly) sets more than one. Both delegate to kind() as the
// single source of truth for precedence, so this can no longer drift.
func TestWorkspaceSource_TypeAndStringAgreeOnKindPrecedence(t *testing.T) {
	// Image and Snapshot both set: kind() must pick one (Image, since it is
	// checked first), and both Type() and String() must agree with that
	// choice and with each other.
	src := WorkspaceSource{
		Image:    "ghcr.io/acme/img:1.0",
		Snapshot: testProviderSnapRef,
	}

	if src.Type() != WorkspaceSourceImage {
		t.Fatalf("got type %q, want %q", src.Type(), WorkspaceSourceImage)
	}
	if src.String() != WorkspaceSourceImage+"ghcr.io/acme/img:1.0" {
		t.Fatalf("got string %q", src.String())
	}
}

func TestWorkspaceSource_KindPrecedenceOrder(t *testing.T) {
	all := WorkspaceSource{
		GitRepository: "https://github.com/acme/repo",
		LocalFolder:   "/home/me/project",
		Image:         "ghcr.io/acme/img:1.0",
		Container:     "my-container",
		Snapshot:      testProviderSnapRef,
	}
	if got, want := all.kind(), workspaceSourceKindGit; got != want {
		t.Fatalf("kind() = %v, want %v (git takes precedence over all others)", got, want)
	}

	noGit := all
	noGit.GitRepository = ""
	if got, want := noGit.kind(), workspaceSourceKindLocal; got != want {
		t.Fatalf("kind() = %v, want %v", got, want)
	}

	noLocal := noGit
	noLocal.LocalFolder = ""
	if got, want := noLocal.kind(), workspaceSourceKindImage; got != want {
		t.Fatalf("kind() = %v, want %v", got, want)
	}

	noImage := noLocal
	noImage.Image = ""
	if got, want := noImage.kind(), workspaceSourceKindContainer; got != want {
		t.Fatalf("kind() = %v, want %v", got, want)
	}

	onlySnapshot := WorkspaceSource{Snapshot: all.Snapshot}
	if got, want := onlySnapshot.kind(), workspaceSourceKindSnapshot; got != want {
		t.Fatalf("kind() = %v, want %v", got, want)
	}

	empty := WorkspaceSource{}
	if got, want := empty.kind(), workspaceSourceKindNone; got != want {
		t.Fatalf("kind() = %v, want %v", got, want)
	}
}
