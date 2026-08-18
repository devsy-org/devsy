package credentials

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func portFromURL(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return port
}

func TestPostWithRetry_ReturnsBodyOnSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	t.Cleanup(server.Close)

	out, err := PostWithRetry(portFromURL(t, server.URL), "endpoint", http.NoBody)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), out)
}

func TestPostWithRetry_ReturnsErrorOnNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	start := time.Now()
	out, err := PostWithRetry(portFromURL(t, server.URL), "endpoint", http.NoBody)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Nil(t, out)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "endpoint")

	assert.Less(t, elapsed, 500*time.Millisecond, "non-200 response must not trigger retries")
}

func TestPostWithRetry_RetriesConnectionRefusedThenSucceeds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	srvCh := make(chan *http.Server, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		srvLn, listenErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if listenErr != nil {
			srvCh <- nil
			return
		}
		srv := &http.Server{
			ReadHeaderTimeout: 5 * time.Second,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("recovered"))
			}),
		}
		srvCh <- srv
		_ = srv.Serve(srvLn)
	}()
	t.Cleanup(func() {
		if srv := <-srvCh; srv != nil {
			_ = srv.Close()
		}
	})

	out, err := PostWithRetry(port, "endpoint", http.NoBody)
	require.NoError(t, err)
	assert.Equal(t, []byte("recovered"), out)
}

func TestPostWithRetry_ExhaustsRetriesOnConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	start := time.Now()
	out, err := PostWithRetry(port, "endpoint", http.NoBody)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Nil(t, out)
	assert.True(t, errors.Is(err, syscall.ECONNREFUSED),
		"error must wrap ECONNREFUSED after exhausting retries, got: %v", err)

	assert.Greater(
		t,
		elapsed,
		200*time.Millisecond,
		"connection-refused must be retried before failing",
	)
}
