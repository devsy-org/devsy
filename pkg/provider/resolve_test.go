package provider

import (
	"testing"
)

const (
	githubPath      = "devsy-org/devsy-provider-ssh"
	canonicalGithub = "github.com/" + githubPath
)

type providerSourceCase struct {
	name string
	src  ProviderSource
	want string
}

func githubCanonicalizationCases() []providerSourceCase {
	return append(githubSourceCases(), otherSourceCases()...)
}

func githubSourceCases() []providerSourceCase {
	return []providerSourceCase{
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
			name: "pinned full GitHub path retains its tag",
			src: ProviderSource{
				Github: githubPath,
				Raw:    "github.com/" + githubPath + "@v1.2.3",
			},
			want: canonicalGithub + "@v1.2.3",
		},
		{
			name: "pinned shorthand uses canonical repository path",
			src: ProviderSource{
				Github: githubPath,
				Raw:    "devsy-provider-ssh@v1.2.3",
			},
			want: canonicalGithub + "@v1.2.3",
		},
		{
			name: "raw tag replaces a legacy GitHub tag without duplication",
			src: ProviderSource{
				Github: canonicalGithub + "@v1.0.0",
				Raw:    "devsy-provider-ssh@v1.2.3",
			},
			want: canonicalGithub + "@v1.2.3",
		},
		{
			name: "legacy GitHub field tag remains when raw is empty",
			src: ProviderSource{
				Github: canonicalGithub + "@v1.0.0",
			},
			want: canonicalGithub + "@v1.0.0",
		},
		{
			name: "unpinned raw source remains canonical and unpinned",
			src: ProviderSource{
				Github: githubPath,
				Raw:    "devsy-provider-ssh",
			},
			want: canonicalGithub,
		},
	}
}

func otherSourceCases() []providerSourceCase {
	return []providerSourceCase{
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
}

func TestGetProviderSource_GithubCanonicalization(t *testing.T) {
	for _, tc := range githubCanonicalizationCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := GetProviderSource(tc.src, "config-name")
			if got != tc.want {
				t.Fatalf("GetProviderSource() = %q, want %q", got, tc.want)
			}
		})
	}
}
