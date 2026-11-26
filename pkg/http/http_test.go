package http

import (
	nethttp "net/http"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

func TestInsecureTLSEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset defaults to secure", value: "", want: false},
		{name: "true enables insecure", value: "true", want: true},
		{name: "1 enables insecure", value: "1", want: true},
		{name: "false stays secure", value: "false", want: false},
		{name: "garbage stays secure", value: "yes-please", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.EnvInsecureTLS, tt.value)
			if got := insecureTLSEnabled(); got != tt.want {
				t.Errorf("insecureTLSEnabled() with %q = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

type idleClosingTransport struct {
	closed bool
}

func (t *idleClosingTransport) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	return nil, nil
}

func (t *idleClosingTransport) CloseIdleConnections() {
	t.closed = true
}

func TestCloseIdleConnections(t *testing.T) {
	mock := &idleClosingTransport{}
	rt := NewStallTransport(mock, time.Second)
	rt = WithUserAgent(rt, "test")
	rt = NewRetryTransport(rt, DefaultRetry)
	client := &nethttp.Client{Transport: rt}
	client.CloseIdleConnections()
	if !mock.closed {
		t.Fatalf("expected CloseIdleConnections forwarded to base transport")
	}
	CloseIdleConnections()
}
