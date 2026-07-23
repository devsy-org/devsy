package http

import (
	"crypto/tls"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
)

var (
	httpClient     *http.Client
	httpClientOnce sync.Once
)

func GetHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		customTransport := http.DefaultTransport.(*http.Transport).Clone()
		if insecureTLSEnabled() {
			// #nosec G402 -- enabled with DEVSY_INSECURE_TLS env
			customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			log.Warnf("TLS certificate verification is disabled (%s)", config.EnvInsecureTLS)
		}
		httpClient = &http.Client{Transport: customTransport}
	})

	return httpClient
}

// insecureTLSEnabled reports whether TLS certificate verification should be
// disabled for the shared HTTP client. Verification is on by default and is
// only disabled when EnvInsecureTLS is set to a truthy value.
func insecureTLSEnabled() bool {
	enabled, _ := strconv.ParseBool(os.Getenv(config.EnvInsecureTLS))
	return enabled
}
