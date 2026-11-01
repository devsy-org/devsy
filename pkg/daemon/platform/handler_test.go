package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandlerFuncServeHTTP(t *testing.T) {
	tests := []struct {
		name     string
		handler  handlerFunc
		wantCode int
		wantBody string
	}{
		{
			name: "plain error is internal server error",
			handler: func(http.ResponseWriter, *http.Request) error {
				return errors.New("boom")
			},
			wantCode: http.StatusInternalServerError,
			wantBody: "boom\n",
		},
		{
			name: "bad request status error keeps its code",
			handler: func(http.ResponseWriter, *http.Request) error {
				return badRequest(errors.New("bad input"))
			},
			wantCode: http.StatusBadRequest,
			wantBody: "bad input\n",
		},
		{
			name: "forbidden status error keeps its code",
			handler: func(http.ResponseWriter, *http.Request) error {
				return forbidden(errors.New("no access"))
			},
			wantCode: http.StatusForbidden,
			wantBody: "no access\n",
		},
		{
			name: "wrapped status error keeps its code and message",
			handler: func(http.ResponseWriter, *http.Request) error {
				return fmt.Errorf("create workspace: %w", badRequest(errors.New("bad input")))
			},
			wantCode: http.StatusBadRequest,
			wantBody: "create workspace: bad input\n",
		},
		{
			name: "nil error leaves handler response untouched",
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte("ok"))
				return nil
			},
			wantCode: http.StatusCreated,
			wantBody: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			require.Equal(t, tt.wantCode, rec.Code)
			require.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestLocalServerMissingQueryParamIsBadRequest(t *testing.T) {
	l := &localServer{}
	tests := []struct {
		name     string
		handler  handlerFunc
		wantBody string
	}{
		{
			name:     "getWorkspace requires uid",
			handler:  l.getWorkspace,
			wantBody: "missing required query parameter \"uid\"\n",
		},
		{
			name:     "getGitCredentials requires host",
			handler:  l.getGitCredentials,
			wantBody: "missing required query parameter \"host\"\n",
		},
		{
			name:     "getDockerCredentials requires server",
			handler:  l.getDockerCredentials,
			wantBody: "missing required query parameter \"server\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}
