package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
)

type healthBody struct {
	io.Reader
	closed bool
}

func (b *healthBody) Close() error {
	b.closed = true
	return nil
}

type healthTransport struct {
	body *healthBody
}

func (t healthTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: t.body, Header: make(http.Header)}, nil
}

func TestHealthClosesResponseBody(t *testing.T) {
	client := devsyhttp.GetHTTPClient()
	originalTransport := client.Transport
	t.Cleanup(func() { client.Transport = originalTransport })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			w,
			`{"kind":"Self","apiVersion":"management.devsy.sh/v1","status":{}}`,
		)
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(fmt.Sprintf(`{"host":%q,"accesskey":"test"}`, server.URL)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	body := &healthBody{Reader: strings.NewReader("healthy")}
	client.Transport = healthTransport{body: body}
	cmd := &HealthCmd{GlobalFlags: &flags.GlobalFlags{Config: configPath}}
	if err := cmd.Run(context.Background(), nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !body.closed {
		t.Fatal("health response body was not closed")
	}
}
