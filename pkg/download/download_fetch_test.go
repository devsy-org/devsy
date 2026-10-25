package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fakeResolver struct {
	cred  string
	err   error
	calls int
}

func (r *fakeResolver) Resolve(_ context.Context, _, _, _ string) (string, string, error) {
	r.calls++
	return "user", r.cred, r.err
}

func TestReleaseURL(t *testing.T) {
	tests := []struct {
		name string
		ref  githubReleaseRef
		want string
	}{
		{
			name: "latest release",
			ref:  githubReleaseRef{org: testGithubOrg, repo: testGithubRepo, file: testGithubFile},
			want: "https://api.github.com/repos/devsy-org/devsy/releases/latest",
		},
		{
			name: "tagged release",
			ref: githubReleaseRef{
				org:     testGithubOrg,
				repo:    testGithubRepo,
				release: testGithubTag,
				file:    testGithubFile,
			},
			want: "https://api.github.com/repos/devsy-org/devsy/releases/tags/v1.2.3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.releaseURL(); got != tt.want {
				t.Errorf("releaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTTPStatusErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *HTTPStatusError
		want string
	}{
		{
			name: "with body",
			err: &HTTPStatusError{
				StatusCode: http.StatusNotFound,
				URL:        testBaseURL,
				Body:       "not found",
			},
			want: "received status code 404 when trying to download " +
				testBaseURL + ": not found",
		},
		{
			name: "without body",
			err:  &HTTPStatusError{StatusCode: http.StatusBadGateway, URL: testBaseURL},
			want: "received status code 502 when trying to download " + testBaseURL,
		},
		{
			name: "userinfo redacted",
			err: &HTTPStatusError{
				StatusCode: http.StatusForbidden,
				URL:        "https://user:tok-7f3a@example.com/file.tgz",
			},
			want: "received status code 403 when trying to download " +
				testBaseURL,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileErrorDoesNotLeakCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "access denied", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	rawURL := "http://user:tok-7f3a@" + strings.TrimPrefix(srv.URL, "http://") + "/file.tgz"
	_, err := File(context.Background(), rawURL)

	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("File() error = %v, want *HTTPStatusError", err)
	}
	if statusErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusForbidden)
	}
	if strings.Contains(err.Error(), "tok-7f3a") {
		t.Errorf("error leaks credential: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("error %q does not include response body", err.Error())
	}
}

func TestFetchGithubPrivateReleaseFallsThrough(t *testing.T) {
	const githubAssetPath = "/devsy-org/devsy/releases/download/v1.2.3/devsy-linux-amd64"
	tests := []struct {
		name      string
		status    int
		path      string
		resolver  *fakeResolver
		wantCalls int
	}{
		{
			name:      "public asset skips resolver",
			status:    http.StatusOK,
			path:      githubAssetPath,
			resolver:  &fakeResolver{cred: "tok-7f3a"},
			wantCalls: 0,
		},
		{
			name:      "not a github release path skips resolver",
			status:    http.StatusNotFound,
			path:      "/file.tgz",
			resolver:  &fakeResolver{cred: "tok-7f3a"},
			wantCalls: 0,
		},
		{
			name:      "resolver error falls through",
			status:    http.StatusNotFound,
			path:      githubAssetPath,
			resolver:  &fakeResolver{err: errors.New("no credentials")},
			wantCalls: 1,
		},
		{
			name:      "empty credential falls through",
			status:    http.StatusNotFound,
			path:      githubAssetPath,
			resolver:  &fakeResolver{},
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.status)
				}),
			)
			t.Cleanup(srv.Close)

			parsed, err := url.Parse(srv.URL + tt.path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := fetchGithubPrivateRelease(context.Background(), parsed, tt.resolver)
			if err != nil {
				t.Fatalf("fetchGithubPrivateRelease() error = %v, want nil", err)
			}
			if body != nil {
				_ = body.Close()
				t.Fatal("fetchGithubPrivateRelease() returned a body, want nil")
			}
			if tt.resolver.calls != tt.wantCalls {
				t.Errorf("resolver calls = %d, want %d", tt.resolver.calls, tt.wantCalls)
			}
		})
	}
}
