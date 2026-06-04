package provider

import (
	"testing"
)

func TestGetProviderSource_GithubCanonicalization(t *testing.T) {
	const githubPath = "devsy-org/devsy-provider-ssh"
	const canonicalGithub = "github.com/" + githubPath

	tests := []struct {
		name string
		src  ProviderSource
		want string
	}{
		{
			name: "bare org/repo gets github.com prefix",
			src:  ProviderSource{Github: githubPath},
			want: canonicalGithub,
		},
		{
			name: "already-prefixed github source is preserved",
			src:  ProviderSource{Github: canonicalGithub},
			want: canonicalGithub,
		},
		{
			name: "internal source returns raw",
			src:  ProviderSource{Internal: true, Raw: "docker"},
			want: "docker",
		},
		{
			name: "url source preserved",
			src:  ProviderSource{URL: "https://example.com/p.yaml"},
			want: "https://example.com/p.yaml",
		},
		{
			name: "file source preserved",
			src:  ProviderSource{File: "/tmp/p.yaml"},
			want: "/tmp/p.yaml",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetProviderSource(tc.src, "config-name")
			if got != tc.want {
				t.Fatalf("GetProviderSource() = %q, want %q", got, tc.want)
			}
		})
	}
}
