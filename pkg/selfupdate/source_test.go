package selfupdate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/google/go-github/v74/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllPagesSourceListReleasesPaginates(t *testing.T) {
	var requestedPages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)
		w.Header().Set("Content-Type", "application/json")

		switch page {
		case "", "1":
			base := "http://" + r.Host + r.URL.Path
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, base))
			_, _ = w.Write([]byte(`[{"tag_name":"v2.0.0-beta.1","prerelease":true}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"tag_name":"v1.0.0","prerelease":false}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	client := github.NewClient(nil)
	baseURL, err := url.Parse(srv.URL + "/")
	require.NoError(t, err)
	client.BaseURL = baseURL

	src := &allPagesSource{api: client}

	releases, err := src.ListReleases(
		context.Background(),
		selfupdate.NewRepositorySlug("owner", "repo"),
	)
	require.NoError(t, err)

	require.Len(t, releases, 2, "should aggregate releases from both pages")
	assert.Equal(t, "v2.0.0-beta.1", releases[0].GetTagName())
	assert.Equal(t, "v1.0.0", releases[1].GetTagName(), "stable release on page 2 must be returned")
	assert.Equal(
		t,
		[]string{"", "2"},
		requestedPages,
		"should request page 1 then follow next to page 2",
	)
}
