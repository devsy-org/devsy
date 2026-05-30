package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListManifestVersions_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/foo/versions.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(versionsManifest{
			Versions: []versionsManifestEntry{
				{Tag: testTagV100, PublishedAt: time.Now(), Prerelease: false},
			},
		})
	}))
	defer server.Close()
	got, err := listManifestVersions(server.URL+"/foo/provider.yaml", false)
	if err != nil || len(got) != 1 || got[0].Tag != testTagV100 {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestListManifestVersions_Missing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	_, err := listManifestVersions(server.URL+"/foo/provider.yaml", false)
	if !errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf("expected ErrVersionListUnsupported, got %v", err)
	}
}
