package provider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

func TestCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVSY_HOME", dir)

	c := ProviderVersionCache{
		"foo": ProviderVersionCacheEntry{
			SourceHash: testNameABC,
			Versions:   []ProviderVersion{{Tag: testTagV100, PublishedAt: time.Now()}},
			FetchedAt:  time.Now(),
		},
	}
	if err := SaveProviderVersionCache(c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProviderVersionCache()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["foo"].SourceHash != testNameABC {
		t.Fatalf("roundtrip mismatch: %+v", loaded)
	}

	wantPath := filepath.Join(dir, "cache", "provider-versions.json")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected cache file at %s (via PathManager.CacheDir()): %v", wantPath, err)
	}
}

func TestProviderVersionCachePath_UsesPathManagerCacheDir(t *testing.T) {
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := providerVersionCachePath()
	if err != nil {
		t.Fatal(err)
	}
	cacheDir, err := config.DefaultPathManager().CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cacheDir, "provider-versions.json")
	if got != want {
		t.Fatalf("providerVersionCachePath() = %q, want %q (PathManager.CacheDir())", got, want)
	}
}

func TestCacheGet_FreshVsStale(t *testing.T) {
	c := ProviderVersionCache{
		"foo": ProviderVersionCacheEntry{SourceHash: testNameABC, FetchedAt: time.Now()},
		testNameBar: ProviderVersionCacheEntry{
			SourceHash: testNameABC,
			FetchedAt:  time.Now().Add(-7 * time.Hour),
		},
	}
	if _, fresh := c.Get("foo", testNameABC); !fresh {
		t.Fatal("expected fresh for foo")
	}
	if _, fresh := c.Get(testNameBar, testNameABC); fresh {
		t.Fatal("expected stale for bar (older than TTL)")
	}
	if _, fresh := c.Get("foo", "different-hash"); fresh {
		t.Fatal("source-hash mismatch must be treated as stale")
	}
}

func TestListVersionsForSourceCached_CachesResults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVSY_HOME", dir)

	source := "github.com/foo/bar@v1.0.0"
	hash := HashProviderSource(source)

	cached := ProviderVersionCache{
		"myprov": {
			SourceHash: hash,
			Versions:   []ProviderVersion{{Tag: testTagV999}},
			FetchedAt:  time.Now(),
		},
	}
	if err := SaveProviderVersionCache(cached); err != nil {
		t.Fatal(err)
	}

	got, err := ListVersionsForSourceCached(
		"myprov",
		source,
		ListVersionsOptions{UseCache: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tag != testTagV999 {
		t.Fatalf("expected cache hit, got %+v", got)
	}
}

func TestListVersionsForSourceCached_BypassesCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVSY_HOME", dir)

	source := "/local/path/provider.yaml"
	hash := HashProviderSource(source)
	cached := ProviderVersionCache{
		"myprov": {
			SourceHash: hash,
			Versions:   []ProviderVersion{{Tag: testTagV999}},
			FetchedAt:  time.Now(),
		},
	}
	if err := SaveProviderVersionCache(cached); err != nil {
		t.Fatal(err)
	}

	_, err := ListVersionsForSourceCached(
		"myprov",
		source,
		ListVersionsOptions{UseCache: false},
	)
	if !errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf(
			"expected ErrVersionListUnsupported when bypassing cache for local source, got %v",
			err,
		)
	}
}

func TestHashProviderSource_Stable(t *testing.T) {
	a := HashProviderSource("github.com/foo/bar@v1.0.0")
	b := HashProviderSource("github.com/foo/bar@v1.0.0")
	if a != b || a == "" {
		t.Fatalf("hash must be stable and non-empty: %q vs %q", a, b)
	}
}
