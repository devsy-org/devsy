//go:build linux

package up

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	runtimeEndpointPath = "/provider/runtime"
	agentEndpointPath   = "/agent/devsy-linux-amd64"
)

func TestRemoteAgentArtifactBaseURLUsesListenerPort(t *testing.T) {
	for _, address := range []string{"0.0.0.0:35437", "[::]:35437", "127.0.0.1:35437", "[::1]:35437"} {
		t.Run(address, func(t *testing.T) {
			got, err := remoteAgentArtifactBaseURL(address)
			if err != nil {
				t.Fatal(err)
			}
			if want := "http://host.docker.internal:35437"; got != want {
				t.Fatalf("artifact base URL = %q, want %q", got, want)
			}
		})
	}
}

func TestRemoteAgentArtifactBaseURLRejectsInvalidAddress(t *testing.T) {
	if _, err := remoteAgentArtifactBaseURL("missing-port"); err == nil {
		t.Fatal("expected an invalid listener address error")
	}
}

func TestRemoteAgentArtifactEndpointsTrackIndependentGETs(t *testing.T) {
	server := &remoteAgentArtifactServer{
		runtime: []byte("runtime binary"),
		agent:   []byte("agent binary"),
	}
	handler := server.artifactHandler()

	assertArtifactResponse(t, handler, artifactResponseExpectation{
		method: http.MethodGet,
		path:   runtimeEndpointPath,
		status: http.StatusOK,
		body:   server.runtime,
	})
	assertArtifactResponse(t, handler, artifactResponseExpectation{
		method: http.MethodGet, path: agentEndpointPath, status: http.StatusOK, body: server.agent,
	})

	if got := server.runtimeRequests.Load(); got != 1 {
		t.Fatalf("runtime GET count = %d, want 1", got)
	}
	if got := server.agentRequests.Load(); got != 1 {
		t.Fatalf("agent GET count = %d, want 1", got)
	}
}

func TestRemoteAgentArtifactEndpointMethodsAndOfflineRuntime(t *testing.T) {
	server := &remoteAgentArtifactServer{
		runtime: []byte("runtime binary"),
		agent:   []byte("agent binary"),
	}
	handler := server.artifactHandler()
	server.offline.Store(true)

	assertArtifactResponse(t, handler, artifactResponseExpectation{
		method: http.MethodGet, path: runtimeEndpointPath, status: http.StatusNotFound,
	})
	assertArtifactResponse(t, handler, artifactResponseExpectation{
		method: http.MethodGet, path: agentEndpointPath, status: http.StatusOK, body: server.agent,
	})
	assertArtifactResponse(t, handler, artifactResponseExpectation{
		method: http.MethodPost, path: runtimeEndpointPath, status: http.StatusMethodNotAllowed,
	})
	assertArtifactResponse(t, handler, artifactResponseExpectation{
		method: http.MethodPost, path: agentEndpointPath, status: http.StatusMethodNotAllowed,
	})

	if got := server.runtimeRequests.Load(); got != 1 {
		t.Fatalf("runtime GET count = %d, want the offline GET only", got)
	}
	if got := server.agentRequests.Load(); got != 1 {
		t.Fatalf("agent GET count = %d, want the successful GET only", got)
	}
}

type artifactResponseExpectation struct {
	method, path string
	status       int
	body         []byte
}

func assertArtifactResponse(t *testing.T, handler http.Handler, want artifactResponseExpectation) {
	t.Helper()
	request := httptest.NewRequest(want.method, want.path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want.status {
		t.Fatalf("%s %s status = %d, want %d", want.method, want.path, response.Code, want.status)
	}
	if want.body != nil && !bytes.Equal(response.Body.Bytes(), want.body) {
		t.Fatalf(
			"%s %s body = %q, want %q",
			want.method,
			want.path,
			response.Body.Bytes(),
			want.body,
		)
	}
}
