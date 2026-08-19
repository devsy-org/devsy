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

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errReader is an io.Reader that always returns an error.
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
	handler := newCredentialsHandler(context.Background(), mock, "alice")

	req := httptest.NewRequest(http.MethodGet, ownerPath, nil)
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
