package workspace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetProjectImage_UnknownHostNeverFetches(t *testing.T) {
	fetched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched = true
	}))
	t.Cleanup(srv.Close)

	got := getProjectImage(srv.URL)
	require.Empty(t, got)
	require.False(t, fetched, "getProjectImage fetched an unrecognized host")
}
