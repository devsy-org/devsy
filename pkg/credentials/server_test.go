package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }

func TestHandleGitSSHSignature_GRPCError_ReturnsJSON500(t *testing.T) {
	mock := &mockCredentialsClient{
		gitSSHSignatureFunc: func(ctx context.Context, msg *tunnel.Message) (*tunnel.Message, error) {
			return nil, fmt.Errorf("Permission denied")
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/git-ssh-signature",
		strings.NewReader("test payload"),
	)
	w := httptest.NewRecorder()

	err := handleGitSSHSignatureRequest(context.Background(), w, req, mock)
	require.NoError(t, err)

	resp := w.Result()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Contains(t, body["error"], "Permission denied")
}

func TestHandleGitSSHSignature_BodyReadError_ReturnsJSON500(t *testing.T) {
	mock := &mockCredentialsClient{
		gitSSHSignatureFunc: func(ctx context.Context, msg *tunnel.Message) (*tunnel.Message, error) {
			t.Fatal("gRPC should not be called when body read fails")
			return nil, nil
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/git-ssh-signature",
		&errReader{err: fmt.Errorf("connection reset")},
	)
	w := httptest.NewRecorder()

	err := handleGitSSHSignatureRequest(context.Background(), w, req, mock)
	require.NoError(t, err, "error should be written to response, not returned")

	resp := w.Result()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err, "response body must be valid JSON")
	assert.Contains(t, body["error"], "connection reset")
}

func TestHandleGitSSHSignature_GRPCSuccess_ReturnsJSON200(t *testing.T) {
	expectedMessage := `{"signature":"abc123"}`
	mock := &mockCredentialsClient{
		gitSSHSignatureFunc: func(ctx context.Context, msg *tunnel.Message) (*tunnel.Message, error) {
			return &tunnel.Message{Message: expectedMessage}, nil
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/git-ssh-signature",
		strings.NewReader("test payload"),
	)
	w := httptest.NewRecorder()

	err := handleGitSSHSignatureRequest(context.Background(), w, req, mock)
	require.NoError(t, err)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "abc123", body["signature"])
}

func TestOwnerEndpoint_ReturnsConfiguredOwner(t *testing.T) {
	mock := &mockCredentialsClient{}
	handler := newCredentialsHandler(mock, "alice")

	req := httptest.NewRequest(http.MethodGet, "http://localhost"+ownerPath, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "alice", string(body))
}

func TestFetchOwner_ReturnsConfiguredOwner(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port

	ctx := t.Context()
	go func() {
		_ = RunCredentialsServerWithListener(ctx, ln, &mockCredentialsClient{}, "bob")
	}()
	require.NoError(t, waitForServer(ctx, port))

	owner, err := FetchOwner(context.Background(), port)
	require.NoError(t, err)
	assert.Equal(t, "bob", owner)
}

func TestFetchOwner_EmptyWhenEndpointMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	var port int
	_, err := fmt.Sscanf(server.URL, "http://127.0.0.1:%d", &port)
	require.NoError(t, err)

	owner, err := FetchOwner(context.Background(), port)
	require.NoError(t, err)
	assert.Empty(t, owner)
}

func TestFetchOwner_DoesNotFollowRedirects(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("evil-owner"))
	}))
	defer evil.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL, http.StatusFound)
	}))
	defer server.Close()

	var port int
	_, err := fmt.Sscanf(server.URL, "http://127.0.0.1:%d", &port)
	require.NoError(t, err)

	owner, err := FetchOwner(context.Background(), port)
	require.NoError(t, err)
	assert.Empty(t, owner, "a redirect must not be followed to another owner value")
}

func TestFetchOwner_CapsResponseSize(t *testing.T) {
	oversized := strings.Repeat("a", maxOwnerResponseSize*2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(oversized))
	}))
	defer server.Close()

	var port int
	_, err := fmt.Sscanf(server.URL, "http://127.0.0.1:%d", &port)
	require.NoError(t, err)

	owner, err := FetchOwner(context.Background(), port)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(owner), maxOwnerResponseSize)
}

func TestCredentialsServerCancelsBackendContext(t *testing.T) {
	type contextKey struct{}
	for _, trigger := range []string{"disconnect", "server shutdown"} {
		t.Run(trigger, func(t *testing.T) {
			started := make(chan context.Context, 1)
			release := make(chan struct{})
			finished := make(chan struct{})
			client := &mockCredentialsClient{
				gitSSHSignatureFunc: func(ctx context.Context, _ *tunnel.Message) (*tunnel.Message, error) {
					started <- ctx
					defer close(finished)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
						return nil, fmt.Errorf("test cleanup")
					}
				},
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(
				context.WithValue(context.Background(), contextKey{}, "value"),
			)
			done := make(chan error, 1)
			go func() { done <- RunCredentialsServerWithListener(ctx, ln, client, "owner") }()
			t.Cleanup(func() {
				close(release)
				cancel()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("server did not stop")
				}
			})
			conn, err := net.Dial("tcp", ln.Addr().String())
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			_, err = fmt.Fprint(
				conn,
				"POST /git-ssh-signature HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n",
			)
			require.NoError(t, err)
			backendCtx := awaitCredentialBackend(t, started)
			require.Equal(t, "value", backendCtx.Value(contextKey{}))
			if trigger == "disconnect" {
				require.NoError(t, conn.Close())
			} else {
				cancel()
			}
			awaitCredentialBackendCanceled(t, finished, backendCtx)
		})
	}
}

func awaitCredentialBackend(t *testing.T, started <-chan context.Context) context.Context {
	t.Helper()
	select {
	case ctx := <-started:
		return ctx
	case <-time.After(2 * time.Second):
		t.Fatal("backend was not called")
		return nil
	}
}

func awaitCredentialBackendCanceled(t *testing.T, finished <-chan struct{}, ctx context.Context) {
	t.Helper()
	select {
	case <-finished:
		require.ErrorIs(t, ctx.Err(), context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("backend context was not canceled")
	}
}
