package workspace

import (
	"errors"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/provider"
)

func TestListVersionsForSource_CachesResults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVSY_HOME", dir)

	source := "github.com/foo/bar@v1.0.0"
	hash := provider.HashProviderSource(source)

	// Prime cache with a synthetic entry that doesn't match any real upstream.
	cached := provider.ProviderVersionCache{
		"myprov": {
			SourceHash: hash,
			Versions:   []provider.ProviderVersion{{Tag: testTagV999}},
			FetchedAt:  time.Now(),
		},
	}
	if err := provider.SaveProviderVersionCache(cached); err != nil {
		t.Fatal(err)
	}

	// listVersionsForSourceCached must read the cache when UseCache is set and the entry is fresh.
	got, err := listVersionsForSourceCached(
		"myprov",
		source,
		provider.ListVersionsOptions{UseCache: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tag != testTagV999 {
		t.Fatalf("expected cache hit, got %+v", got)
	}
}

func TestListVersionsForSource_BypassesCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVSY_HOME", dir)

	source := "/local/path/provider.yaml"
	hash := provider.HashProviderSource(source)
	cached := provider.ProviderVersionCache{
		"myprov": {
			SourceHash: hash,
			Versions:   []provider.ProviderVersion{{Tag: testTagV999}},
			FetchedAt:  time.Now(),
		},
	}
	if err := provider.SaveProviderVersionCache(cached); err != nil {
		t.Fatal(err)
	}

	// With UseCache=false the cache is ignored and the underlying classifier runs.
	// Local source → ErrVersionListUnsupported.
	_, err := listVersionsForSourceCached(
		"myprov",
		source,
		provider.ListVersionsOptions{UseCache: false},
	)
	if !errors.Is(err, provider.ErrVersionListUnsupported) {
		t.Fatalf(
			"expected ErrVersionListUnsupported when bypassing cache for local source, got %v",
			err,
		)
	}
}
