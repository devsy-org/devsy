package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"time"

	devsyhttp "github.com/devsy-org/devsy/pkg/http"
)

type versionsManifestEntry struct {
	Tag         string    `json:"tag"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
}

type versionsManifest struct {
	Versions []versionsManifestEntry `json:"versions"`
}

func listManifestVersions(
	canonicalSource string,
	includePrerelease bool,
) ([]ProviderVersion, error) {
	base, _ := splitSourceAndTag(canonicalSource)
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(path.Dir(u.Path), "versions.json")
	resp, err := devsyhttp.GetHTTPClient().Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("manifest fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	manifest, err := decodeManifest(resp)
	if err != nil {
		return nil, err
	}

	return filterAndSortManifestVersions(manifest, includePrerelease), nil
}

func decodeManifest(resp *http.Response) (*versionsManifest, error) {
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrVersionListUnsupported
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("manifest: status %d", resp.StatusCode)
	}

	var manifest versionsManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &manifest, nil
}

func filterAndSortManifestVersions(
	manifest *versionsManifest,
	includePrerelease bool,
) []ProviderVersion {
	out := make([]ProviderVersion, 0, len(manifest.Versions))
	for _, v := range manifest.Versions {
		if v.Prerelease && !includePrerelease {
			continue
		}
		out = append(out, ProviderVersion{
			Tag:         v.Tag,
			PublishedAt: v.PublishedAt,
			Prerelease:  v.Prerelease,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	return out
}
