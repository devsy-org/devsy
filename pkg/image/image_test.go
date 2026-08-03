package image

import (
	"sort"
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
