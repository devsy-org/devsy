package workspace

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
)

func TestGetProviderSource_GithubCanonicalization(t *testing.T) {
	const githubPath = "devsy-org/devsy-provider-ssh"
	const canonicalGithub = "github.com/" + githubPath

	tests := []struct {
		name string
		src  provider.ProviderSource
		want string
	}{
		{
			name: "bare org/repo gets github.com prefix",
			src:  provider.ProviderSource{Github: githubPath},
			want: canonicalGithub,
		},
		{
			name: "already-prefixed github source is preserved",
			src:  provider.ProviderSource{Github: canonicalGithub},
			want: canonicalGithub,
		},
		{
			name: "internal source returns raw",
			src:  provider.ProviderSource{Internal: true, Raw: "docker"},
			want: "docker",
		},
		{
			name: "url source preserved",
			src:  provider.ProviderSource{URL: "https://example.com/p.yaml"},
			want: "https://example.com/p.yaml",
		},
		{
			name: "file source preserved",
			src:  provider.ProviderSource{File: "/tmp/p.yaml"},
			want: "/tmp/p.yaml",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getProviderSource(tc.src, "config-name")
			if got != tc.want {
				t.Fatalf("getProviderSource() = %q, want %q", got, tc.want)
			}
		})
	}
}
