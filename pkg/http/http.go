package http

import (
	"crypto/tls"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/version"
)

// DefaultResponseHeaderTimeout bounds how long a request waits for response
// headers after it has been written. It bounds unresponsive endpoints without
// affecting body streaming (once headers arrive the timer no longer applies).
// It is unsuitable only for servers that deliberately withhold response headers
// until data is ready — an uncommon long-poll design.
const DefaultResponseHeaderTimeout = 60 * time.Second

var (
	baseTransportOnce sync.Once
	baseTransportInst *http.Transport

	httpClientOnce sync.Once
	httpClientInst *http.Client
)

// baseRoundTripper returns the shared base transport (TLS, proxy, dial and
// header timeouts) with no higher-level behavior. Purpose-built clients compose
// decorators — user-agent, retry, stall — on top of it, sharing its connection
// pool.
func baseRoundTripper() *http.Transport {
	baseTransportOnce.Do(func() {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = DefaultResponseHeaderTimeout
		if insecureTLSEnabled() {
			// #nosec G402 -- enabled with DEVSY_INSECURE_TLS env
			t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			log.Warnf("TLS certificate verification is disabled (%s)", config.EnvInsecureTLS)
		}
		baseTransportInst = t
	})
	return baseTransportInst
}

// GetHTTPClient returns the shared general-purpose client. It sends a
// User-Agent and retries idempotent requests (GET, HEAD) on transient failures
// — connection errors, 5xx, and 429 — honoring the server's Retry-After hint.
//
// It is not intended for long-lived streaming responses that withhold headers;
// build a dedicated client for those.
func GetHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		rt := WithUserAgent(baseRoundTripper(), UserAgent())
		rt = NewRetryTransport(rt, DefaultRetry)
		httpClientInst = &http.Client{Transport: rt}
	})
	return httpClientInst
}

// UserAgent is the value sent on requests made through the package's clients.
func UserAgent() string {
	return config.BinaryName + "/" + version.GetVersion()
}

// insecureTLSEnabled reports whether TLS certificate verification should be
// disabled for the shared HTTP client. Verification is on by default and is
// only disabled when EnvInsecureTLS is set to a truthy value.
func insecureTLSEnabled() bool {
	enabled, _ := strconv.ParseBool(os.Getenv(config.EnvInsecureTLS))
	return enabled
}
