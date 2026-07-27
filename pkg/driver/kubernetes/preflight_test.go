package kubernetes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func newTestDriver(t *testing.T, host string) *KubernetesDriver {
	t.Helper()
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: host})
	if err != nil {
		t.Fatalf("NewForConfig: %v", err)
	}
	return &KubernetesDriver{client: &Client{client: cs, config: &rest.Config{Host: host}}}
}

func TestKubernetesPreflightReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"major":"1","minor":"29","gitVersion":"v1.29.0"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := newTestDriver(
		t,
		srv.URL,
	).Preflight(context.Background(), driver.PreflightOptions{}); err != nil {
		t.Fatalf("Preflight against reachable API = %v, want nil", err)
	}
}

func TestKubernetesPreflightUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	host := srv.URL
	srv.Close() // nothing is listening on host anymore

	err := newTestDriver(t, host).Preflight(context.Background(), driver.PreflightOptions{})
	var perr *driver.PreflightError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *driver.PreflightError for unreachable cluster, got %v (%T)", err, err)
	}
}
