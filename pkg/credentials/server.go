package credentials

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"google.golang.org/grpc"
)

const DefaultPort = "12049"

type CredentialsClient interface {
	GitCredentials(
		ctx context.Context, in *tunnel.Message, opts ...grpc.CallOption,
	) (*tunnel.Message, error)
	DockerCredentials(
		ctx context.Context, in *tunnel.Message, opts ...grpc.CallOption,
	) (*tunnel.Message, error)
	GitSSHSignature(
		ctx context.Context, in *tunnel.Message, opts ...grpc.CallOption,
	) (*tunnel.Message, error)
	GPGPublicKeys(
		ctx context.Context, in *tunnel.Message, opts ...grpc.CallOption,
	) (*tunnel.Message, error)
	DevsyConfig(
		ctx context.Context, in *tunnel.Message, opts ...grpc.CallOption,
	) (*tunnel.Message, error)
}

var _ CredentialsClient = (tunnel.TunnelClient)(nil)

func RunCredentialsServer(
	ctx context.Context,
	port int,
	client CredentialsClient,
) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("localhost", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", port, err)
	}
	return RunCredentialsServerWithListener(ctx, ln, client)
}

// RunCredentialsServerWithListener is like RunCredentialsServer, but takes an
// already-bound listener. Use this when the caller must hold the port
// exclusively (via net.Listen) from before startup through to serving, so no
// other process can bind the same port in between.
func RunCredentialsServerWithListener(
	ctx context.Context,
	ln net.Listener,
	client CredentialsClient,
) error {
	srv := &http.Server{
		Handler:           newCredentialsHandler(ctx, client),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
		log.Debugf("credentials server started: addr=%v", ln.Addr())

		// always returns error. ErrServerClosed on graceful close
		if err := srv.Serve(ln); err != http.ErrServerClosed {
			errChan <- err
		} else {
			errChan <- nil
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		_ = srv.Close()
		return nil
	}
}

type credentialsHandlerFunc func(
	context.Context, http.ResponseWriter, *http.Request, CredentialsClient,
) error

func newCredentialsHandler(ctx context.Context, client CredentialsClient) http.Handler {
	routes := map[string]credentialsHandlerFunc{
		// Root is a readiness probe (see waitForServer); it must return 200 so the
		// server is detected as up. Unknown paths still 404 below.
		"/": func(_ context.Context, writer http.ResponseWriter, _ *http.Request, _ CredentialsClient) error {
			writer.WriteHeader(http.StatusOK)
			return nil
		},
		"/git-credentials":            handleGitCredentialsRequest,
		"/docker-credentials":         handleDockerCredentialsRequest,
		"/git-ssh-signature":          handleGitSSHSignatureRequest,
		"/devsy-platform-credentials": handleDevsyPlatformCredentialsRequest,
		"/gpg-public-keys": func(
			ctx context.Context, writer http.ResponseWriter, _ *http.Request, client CredentialsClient,
		) error {
			return handleGPGPublicKeysRequest(ctx, writer, client)
		},
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		log.Debugf("incoming client connection: path=%s", request.URL.Path)
		handler, ok := routes[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		if err := handler(ctx, writer, request, client); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
		}
	})
}

func GetPort() (int, error) {
	strPort := cmp.Or(os.Getenv(config.EnvCredentialsServerPort), DefaultPort)
	port, err := strconv.Atoi(strPort)
	if err != nil {
		return 0, fmt.Errorf("convert port %s: %w", strPort, err)
	}

	return port, nil
}

func handleDockerCredentialsRequest(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	client CredentialsClient,
) error {
	out, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}

	log.Debugf("received docker credentials post data: bytes=%d", len(out))
	response, err := client.DockerCredentials(ctx, &tunnel.Message{Message: string(out)})
	if err != nil {
		return fmt.Errorf("get docker credentials response: %w", err)
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(response.Message))
	log.Debugf("wrote docker credentials response: bytes=%v", len(response.Message))
	return nil
}

func handleGitCredentialsRequest(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	client CredentialsClient,
) error {
	out, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}

	log.Debugf("received git credentials post data: bytes=%d", len(out))
	response, err := client.GitCredentials(ctx, &tunnel.Message{Message: string(out)})
	if err != nil {
		log.Debugf("error receiving git credentials: error=%v", err)
		return fmt.Errorf("get git credentials response: %w", err)
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(response.Message))
	log.Debugf("wrote git credentials response: bytes=%v", len(response.Message))
	return nil
}

func handleGitSSHSignatureRequest(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	client CredentialsClient,
) error {
	out, err := io.ReadAll(request.Body)
	if err != nil {
		log.Errorf("error reading git SSH signature request body: %v", err)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		errJSON, _ := json.Marshal(
			map[string]string{"error": fmt.Sprintf("read request body: %v", err)},
		)
		_, _ = writer.Write(errJSON)
		return nil
	}

	log.Debugf("received git SSH signature post data: bytes=%d", len(out))
	response, err := client.GitSSHSignature(ctx, &tunnel.Message{Message: string(out)})
	if err != nil {
		log.Errorf("error receiving git SSH signature: error=%v", err)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		errJSON, _ := json.Marshal(map[string]string{"error": err.Error()})
		_, _ = writer.Write(errJSON)
		return nil // error already written to response
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(response.Message))
	log.Debugf("wrote git SSH signature response: bytes=%v", len(response.Message))
	return nil
}

func handleDevsyPlatformCredentialsRequest(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	client CredentialsClient,
) error {
	out, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}

	log.Debugf("received devsy platform credentials post data: bytes=%d", len(out))
	response, err := client.DevsyConfig(ctx, &tunnel.Message{Message: string(out)})
	if err != nil {
		log.Errorf("error receiving platform credentials: error=%v", err)
		return fmt.Errorf("get platform credentials: %w", err)
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(response.Message))
	log.Debugf("wrote platform credentials response: bytes=%v", len(response.Message))
	return nil
}

func handleGPGPublicKeysRequest(
	ctx context.Context,
	writer http.ResponseWriter,
	client CredentialsClient,
) error {
	response, err := client.GPGPublicKeys(ctx, &tunnel.Message{})
	if err != nil {
		log.Errorf("error receiving GPG public keys: error=%v", err)
		return fmt.Errorf("get gpg public keys: %w", err)
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(response.Message))
	log.Debugf("wrote GPG public keys response: bytes=%v", len(response.Message))
	return nil
}
