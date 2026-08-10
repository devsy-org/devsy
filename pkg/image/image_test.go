package image

import (
	"sort"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestPlatformsFromManifests_FiltersUnknownAndDedupes(t *testing.T) {
	manifests := []v1.Descriptor{
		{Platform: &v1.Platform{OS: osLinux, Architecture: "amd64"}},
		{Platform: &v1.Platform{OS: osLinux, Architecture: "arm64"}},
		{Platform: &v1.Platform{OS: osLinux, Architecture: "amd64"}}, // dup
		{Platform: &v1.Platform{OS: osUnknown, Architecture: osUnknown}},
		{Platform: nil},
	}
	got := platformsFromManifests(manifests)
	sort.Strings(got)
	want := []string{"linux/amd64", "linux/arm64"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// pkg/image's own reference parsing must stay secure-by-default: the
// host.docker.internal insecure-registry override belongs only to
// pkg/snapshot's local test-fixture needs (see pkg/snapshot/reference.go),
// not to general image operations (devcontainer image/feature pulls,
// `devsy build` push checks, etc.). A host.docker.internal reference parsed
// by this package must behave identically to any other registry host —
// i.e. name.ParseReference without name.Insecure, which rejects an HTTP-only
// scheme override and defaults to HTTPS.
func TestParseReference_DoesNotSpecialCaseDockerInternalHost(t *testing.T) {
	secure, err := name.ParseReference("host.docker.internal:5000/acme/repo:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	insecure, err := name.ParseReference("host.docker.internal:5000/acme/repo:tag", name.Insecure)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secure.Context().RegistryStr() == "" {
		t.Fatalf("expected a non-empty registry string")
	}
	// name.Insecure changes the scheme returned by Context().Scheme() from
	// https to http; a package-level wrapper that silently injected
	// name.Insecure for this host (the removed pkg/image/reference.go) would
	// make GetImage/CheckPushPermissions etc. behave like the `insecure`
	// reference below for every caller. Asserting they differ pins down that
	// this package's parsing is unaffected by the host string.
	if secure.Context().Scheme() == insecure.Context().Scheme() {
		t.Fatalf(
			"expected secure and insecure parses of a host.docker.internal ref to use different schemes, both got %q",
			secure.Context().Scheme(),
		)
	}
}

func TestIsValidDockerTag(t *testing.T) {
	// Docker tag grammar: [\w][\w.-]{0,127} — a single word char then up to 127
	// word/dot/hyphen chars. pkg/image accepts an empty string as valid because the
	// build caller (cmd/workspace/build.go) only invokes ValidateTags when the tag
	// slice is non-empty, so an empty element means "no tag"; pin that here.
	longTag := strings.Repeat("a", DockerTagMaxSize)
	overlongTag := strings.Repeat("a", DockerTagMaxSize+1)
	const latestTag = "latest"

	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{"empty accepted as no-tag sentinel", "", true},
		{"single char", "a", true},
		{latestTag, latestTag, true},
		{"semver", "v1.0.0", true},
		{"numeric", "3.18", true},
		{"dotted and hyphenated", "ubuntu-20.04", true},
		{"underscore", "a_b", true},
		{"at max size", longTag, true},
		{"leading dot", ".latest", false},
		{"leading hyphen", "-v1", false},
		{"slash", "foo/bar", false},
		{"colon", "v1:0", false},
		{"space", "tag with space", false},
		{"at sign", "tag@2", false},
		{"non-ascii", "α", false},
		{"over max size", overlongTag, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidDockerTag(tc.tag); got != tc.want {
				t.Errorf("IsValidDockerTag(%q) = %v, want %v", tc.tag, got, tc.want)
			}
		})
	}
}

func TestValidateTags(t *testing.T) {
	t.Run("all valid", func(t *testing.T) {
		if err := ValidateTags([]string{"latest", "v1.0.0", "3.18"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		if err := ValidateTags(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects invalid tag with offending value in message", func(t *testing.T) {
		err := ValidateTags([]string{"latest", "foo/bar"})
		if err == nil {
			t.Fatal("expected error for invalid tag, got nil")
		}
		if !strings.Contains(err.Error(), `"foo/bar"`) {
			t.Errorf("expected error to quote the offending tag %q, got: %v", "foo/bar", err)
		}
	})

	t.Run("reports first invalid tag", func(t *testing.T) {
		err := ValidateTags([]string{".bad", "-also-bad"})
		if err == nil {
			t.Fatal("expected error for invalid tag, got nil")
		}
		if !strings.Contains(err.Error(), `".bad"`) {
			t.Errorf("expected first invalid tag in error, got: %v", err)
		}
	})
}
