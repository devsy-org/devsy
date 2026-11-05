package pro

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestHostReachabilityClosesConnection(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			closed := make(chan struct{})
			var once sync.Once
			server := httptest.NewUnstartedServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"version":"v1.0.0"}`))
				}),
			)
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateClosed {
					once.Do(func() { close(closed) })
				}
			}
			server.StartTLS()
			defer server.Close()

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			reachable, err := isHostReachable(ctx, strings.TrimPrefix(server.URL, "https://"))
			require.NoError(t, err)
			require.Equal(t, status == http.StatusOK, reachable)
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("reachability probe retained its connection after returning")
			}
		})
	}
}

func TestRouterProbeClosesFailedResponseBeforeRetry(t *testing.T) {
	closed := make(chan struct{})
	var once sync.Once
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		once.Do(func() { close(closed) })
	}))
	defer server.Close()
	domain := strings.TrimPrefix(server.URL, "https://")
	cmd := &StartCmd{
		KubeClient: fake.NewClientset(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: DevsyRouterDomainSecret, Namespace: "probe"},
			Data:       map[string][]byte{"domain": []byte(domain)},
		}),
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	done := make(chan struct{})
	defer func() {
		cancel()
		<-done
	}()
	go func() {
		defer close(done)
		_, _ = cmd.pingDevsyRouter(
			ctx,
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "probe"}},
		)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("router probe retained a failed response during the retry delay")
	}
}

func TestLoginClosesTransportOnInvalidResponse(t *testing.T) {
	closed := make(chan struct{})
	var once sync.Once
	server := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}),
	)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			once.Do(func() { close(closed) })
		}
	}
	server.StartTLS()
	defer server.Close()

	cmd := &StartCmd{}
	require.Error(t, cmd.loginViaCLI(server.URL))
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("login retained its transport connection after returning")
	}
}
