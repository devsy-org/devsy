package ts

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// CheckDerpConnection validates the DERP connection. insecure skips TLS
// certificate verification, for coordinators known to use self-signed certs.
func CheckDerpConnection(ctx context.Context, baseUrl *url.URL, insecure bool) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		} // #nosec G402 -- opt-in via explicit insecure flag
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	derpUrl := *baseUrl
	derpUrl.Path = "/derp/probe"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, derpUrl.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach the coordinator server: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("coordinator server returned status %d", res.StatusCode)
	}

	return nil
}

func GetEnvOrDefault(envVar, defaultVal string) string {
	if val := os.Getenv(envVar); val != "" {
		return val
	}
	return defaultVal
}

func RemoveProtocol(hostPath string) string {
	if _, after, ok := strings.Cut(hostPath, "://"); ok {
		return after
	}
	return hostPath
}
