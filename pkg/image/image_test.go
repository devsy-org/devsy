package image

import (
	"sort"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestPlatformsFromIndex_FiltersUnknownAndDedupes(t *testing.T) {
	manifests := []v1.Descriptor{
		{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}},
		{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}},
		{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}, // dup
		{Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"}},
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
