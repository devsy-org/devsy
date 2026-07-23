package http

import "net/http"

// WithUserAgent wraps base so requests that don't already carry a User-Agent
// header get the given value. An empty agent returns base unchanged.
func WithUserAgent(base http.RoundTripper, agent string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if agent == "" {
		return base
	}
	return &userAgentTransport{base: base, agent: agent}
}

type userAgentTransport struct {
	base  http.RoundTripper
	agent string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.agent)
	}
	return t.base.RoundTrip(req)
}
