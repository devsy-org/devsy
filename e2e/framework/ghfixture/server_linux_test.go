//go:build linux

package ghfixture

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIOracleRejectsWrongHostTokensForRESTAndGraphQL(t *testing.T) {
	root := t.TempDir()
	cert := filepath.Join(root, "cert.pem")
	accounts := Accounts{}
	for _, host := range Hosts {
		accounts[host] = Tokens{
			Forwarded: "dummy-" + host,
			Local:     "local-" + host,
			Second:    "second-" + host,
			Invalid:   "invalid-" + host,
			Refresh:   "refresh-" + host,
		}
	}
	server, err := StartAPI(root, cert, "127.0.0.1:0", accounts)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, server.Close(ctx))
	})
	proxy, err := url.Parse("http://" + server.Address())
	require.NoError(t, err)
	pub, err := readFixture(cert)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(pub))
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxy),
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		Timeout: time.Second,
	}
	t.Cleanup(client.CloseIdleConnections)
	checkAPIResponses(t, client, accounts)
	checkAPIAuthorities(t, client)
	server.mu.Lock()
	observations := append([]Observation(nil), server.observations...)
	server.mu.Unlock()
	data, err := json.Marshal(observations)
	require.NoError(t, err)
	for _, record := range accounts {
		require.False(
			t,
			strings.Contains(string(data), record.Forwarded),
			"oracle observation exposed a token",
		)
	}
}

func checkAPIAuthorities(t *testing.T, client *http.Client) {
	t.Helper()
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"https://api.github.com/user",
		nil,
	)
	require.NoError(t, err)
	req.Host = Hosts[1]
	response, err := client.Do(req)
	require.NoError(t, err)
	_, readErr := io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	require.NoError(t, readErr)
	require.Equal(
		t,
		http.StatusForbidden,
		response.StatusCode,
		"inner HTTPS authority must match CONNECT authority",
	)
	req, err = http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"https://unregistered.invalid/user",
		nil,
	)
	require.NoError(t, err)
	response, err = client.Do(req)
	if response != nil {
		_ = response.Body.Close()
	}
	require.Error(t, err, "CONNECT must reject an authority outside the fixture allowlist")
}

func checkAPIResponses(t *testing.T, client *http.Client, accounts Accounts) {
	t.Helper()
	for _, host := range Hosts {
		for _, route := range []string{"/user", "/graphql"} {
			for _, correct := range []bool{false, true} {
				token := accounts[host].Forwarded
				if !correct {
					token = accounts[Hosts[0]].Forwarded
					if host == Hosts[0] {
						token = accounts[Hosts[1]].Forwarded
					}
				}
				req, err := http.NewRequestWithContext(
					context.Background(),
					http.MethodPost,
					"https://"+host+route,
					strings.NewReader(`{"query":"{viewer{login}}"}`),
				)
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+token)
				response, err := client.Do(req)
				require.NoError(t, err)
				data, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				require.NoError(t, readErr)
				var body map[string]any
				require.NoError(t, json.Unmarshal(data, &body))
				want := http.StatusUnauthorized
				if correct {
					want = http.StatusOK
				}
				require.Equal(t, want, response.StatusCode)
			}
		}
	}
}
