package workspace

import (
	"errors"
	"testing"
	"time"
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

func TestRewriteSourceTag(t *testing.T) {
	got, err := rewriteSourceTag("github.com/foo/bar@v1.0.0", "v2.0.0")
	if err != nil || got != "github.com/foo/bar@v2.0.0" {
		t.Fatalf("got %q err %v", got, err)
	}
	got, err = rewriteSourceTag("github.com/foo/bar", "v2.0.0")
	if err != nil || got != "github.com/foo/bar@v2.0.0" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := rewriteSourceTag("github.com/foo/bar", ""); err == nil {
		t.Fatal("empty tag must error")
	}
}

func TestRewriteSourceTag_RejectsAmbiguousAt(t *testing.T) {
	// If splitSourceAndTag yields a base that itself contains @, we cannot safely swap.
	// Construct such a case: source with two @ signs.
	// splitSourceAndTag splits on the FIRST @, so for "a@b@c", base="a", tag="b@c".
	// base="a" does NOT contain @ — so this case passes. To get a base WITH @ we'd need a
	// source like "a@@b" — base="a", tag="@b". Still base has no @.
	// The check is defensive; without splitSourceAndTag changing semantics, this branch
	// is hard to trigger naturally. Skip if not triggerable.
	t.Skip("base-with-@ branch is defensive; not triggerable via splitSourceAndTag")
}

func TestListVersionsForSource_CachesResults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVSY_HOME", dir)

	source := "github.com/foo/bar@v1.0.0"
	hash := hashProviderSource(source)

	// Prime cache with a synthetic entry that doesn't match any real upstream.
	cached := providerVersionCache{
		"myprov": {
			SourceHash: hash,
			Versions:   []ProviderVersion{{Tag: "v9.9.9"}},
			FetchedAt:  time.Now(),
		},
	}
	if err := SaveProviderVersionCache(cached); err != nil {
		t.Fatal(err)
	}

	// listVersionsForSourceCached must read the cache when UseCache is set and the entry is fresh.
	got, err := listVersionsForSourceCached("myprov", source, ListVersionsOptions{UseCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tag != "v9.9.9" {
		t.Fatalf("expected cache hit, got %+v", got)
	}
}

func TestListVersionsForSource_BypassesCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVSY_HOME", dir)

	source := "/local/path/provider.yaml"
	hash := hashProviderSource(source)
	cached := providerVersionCache{
		"myprov": {
			SourceHash: hash,
			Versions:   []ProviderVersion{{Tag: "v9.9.9"}},
			FetchedAt:  time.Now(),
		},
	}
	if err := SaveProviderVersionCache(cached); err != nil {
		t.Fatal(err)
	}

	// With UseCache=false the cache is ignored and the underlying classifier runs.
	// Local source → ErrVersionListUnsupported.
	_, err := listVersionsForSourceCached("myprov", source, ListVersionsOptions{UseCache: false})
	if !errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf(
			"expected ErrVersionListUnsupported when bypassing cache for local source, got %v",
			err,
		)
	}
}
