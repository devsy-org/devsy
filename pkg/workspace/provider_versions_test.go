package workspace

import (
	"errors"
	"testing"
)

func TestErrVersionListUnsupported(t *testing.T) {
	if ErrVersionListUnsupported == nil {
		t.Fatal("ErrVersionListUnsupported must be defined")
	}
	wrapped := errors.New("wrapped: " + ErrVersionListUnsupported.Error())
	if !errors.Is(errors.Join(ErrVersionListUnsupported, wrapped), ErrVersionListUnsupported) {
		t.Fatal("errors.Is must work against ErrVersionListUnsupported")
	}
}

func TestProviderVersionFields(t *testing.T) {
	v := ProviderVersion{Tag: "v1.0.0", Prerelease: false, Current: true}
	if v.Tag != "v1.0.0" || !v.Current {
		t.Fatal("fields must round-trip")
	}
}

func TestClassifyVersionSource(t *testing.T) {
	cases := []struct {
		in   string
		kind sourceKind
	}{
		{"github.com/devsy-org/devsy-provider-aws@v1.2.0", sourceGitHub},
		{"github.com/devsy-org/devsy-provider-aws", sourceGitHub},
		{"https://example.com/foo/provider.yaml", sourceManifestURL},
		{"https://example.com/foo/provider.yaml@v1.0.0", sourceManifestURL},
		{"/abs/path/provider.yaml", sourceLocal},
		{"./relative/provider.yaml", sourceLocal},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := classifyVersionSource(c.in)
			if got != c.kind {
				t.Fatalf("got %v, want %v", got, c.kind)
			}
		})
	}
}

func TestListVersionsForSource_LocalUnsupported(t *testing.T) {
	_, err := listVersionsForSource("/abs/path/provider.yaml", ListVersionsOptions{})
	if !errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf("local source must be unsupported, got %v", err)
	}
}

func TestListVersionsForSource_UnknownUnsupported(t *testing.T) {
	_, err := listVersionsForSource("totally-bogus-source", ListVersionsOptions{})
	if !errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf("unknown source must be unsupported, got %v", err)
	}
}

func TestListVersionsForSource_GitHubInvalid(t *testing.T) {
	_, err := listVersionsForSource("github.com/missingrepo", ListVersionsOptions{})
	if err == nil || errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf("github source missing repo segment must error (not unsupported): %v", err)
	}
}

func TestMarkCurrent(t *testing.T) {
	versions := []ProviderVersion{{Tag: "v1.0.0"}, {Tag: "v0.9.0"}}
	got := markCurrent(versions, "github.com/foo/bar@v1.0.0")
	if !got[0].Current || got[1].Current {
		t.Fatalf("only v1.0.0 should be marked current: %+v", got)
	}
}

func TestMarkCurrent_NoTag(t *testing.T) {
	versions := []ProviderVersion{{Tag: "v1.0.0"}}
	got := markCurrent(versions, "github.com/foo/bar")
	if got[0].Current {
		t.Fatal("no pinned tag → none current")
	}
}
