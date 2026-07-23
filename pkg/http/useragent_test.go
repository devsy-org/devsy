package http

import (
	"net/http"
	"testing"
)

type recordingRoundTripper struct{ lastUA string }

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.lastUA = req.Header.Get("User-Agent")
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func TestWithUserAgentSetsHeaderWhenAbsent(t *testing.T) {
	rec := &recordingRoundTripper{}
	rt := WithUserAgent(rec, "devsy/test")

	req, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if rec.lastUA != "devsy/test" {
		t.Fatalf("expected user-agent set, got %q", rec.lastUA)
	}
	if req.Header.Get("User-Agent") != "" {
		t.Fatalf("original request must not be mutated, got %q", req.Header.Get("User-Agent"))
	}
}

func TestWithUserAgentPreservesExisting(t *testing.T) {
	rec := &recordingRoundTripper{}
	rt := WithUserAgent(rec, "devsy/test")

	req, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	req.Header.Set("User-Agent", "caller/1.0")
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if rec.lastUA != "caller/1.0" {
		t.Fatalf("expected caller user-agent preserved, got %q", rec.lastUA)
	}
}

func TestWithUserAgentEmptyReturnsBase(t *testing.T) {
	base := http.DefaultTransport
	if got := WithUserAgent(base, ""); got != base {
		t.Fatalf("expected base returned for empty agent")
	}
}
