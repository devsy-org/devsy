package http

import (
	"crypto/tls"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/version"
)

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
		if insecureTLSEnabled() {
			// #nosec G402 -- enabled with DEVSY_INSECURE_TLS env
			t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			log.Warnf("TLS certificate verification is disabled (%s)", config.EnvInsecureTLS)
		}
		baseTransportInst = t
	})
	return baseTransportInst
}

// GetHTTPClient returns the shared client: base transport, stall guard,
// user-agent, and retry of idempotent requests. The stall guard bounds idle
// header/body waits, so it must not be used for intentionally-idle streams.
func GetHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		rt := NewStallTransport(baseRoundTripper(), DefaultStallTimeout)
		rt = WithUserAgent(rt, UserAgent())
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
