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

const DefaultResponseHeaderTimeout = 60 * time.Second

var (
	baseTransportOnce sync.Once
	baseTransportInst *http.Transport

	httpClientOnce sync.Once
	httpClientInst *http.Client
)

// baseRoundTripper is the shared transport that purpose-built clients wrap.
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

// GetHTTPClient returns the shared client: base transport, user-agent, and
// retry of idempotent requests. Not for streaming responses that withhold headers.
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
