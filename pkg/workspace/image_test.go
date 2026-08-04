package workspace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetProjectImage_UnknownHostNeverFetches(t *testing.T) {
	// getProjectImage is reachable with a user-supplied link (see
	// resolveWorkspaceSource), so a host outside regexes' known keys must
	// short-circuit before ever calling http.Get -- otherwise this is an
	// SSRF: an attacker-controlled URL would be fetched regardless of
	// whether the response is later discarded for an unrecognized host.
	fetched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched = true
	}))
	t.Cleanup(srv.Close)

	got := getProjectImage(srv.URL)
	require.Empty(t, got)
	require.False(t, fetched, "getProjectImage fetched an unrecognized host")
}
