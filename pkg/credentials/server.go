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

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
)

const DefaultPort = "12049"

func RunCredentialsServer(
	ctx context.Context,
	port int,
	client tunnel.TunnelClient,
) error {
	var handler http.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		log.Debugf("incoming client connection: path=%s", request.URL.Path)
		switch request.URL.Path {
		case "/git-credentials":
			err := handleGitCredentialsRequest(ctx, writer, request, client)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
		case "/docker-credentials":
			err := handleDockerCredentialsRequest(ctx, writer, request, client)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
		case "/git-ssh-signature":
			err := handleGitSSHSignatureRequest(ctx, writer, request, client)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
		case "/devsy-platform-credentials":
			err := handleDevsyPlatformCredentialsRequest(ctx, writer, request, client)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
			}
		case "/gpg-public-keys":
			err := handleGPGPublicKeysRequest(ctx, writer, client)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
			}
		}
	})

	addr := net.JoinHostPort("localhost", strconv.Itoa(port))
	srv := &http.Server{Addr: addr, Handler: handler}

	errChan := make(chan error, 1)
	go func() {
		log.Debugf("credentials server started: port=%v", port)

		// always returns error. ErrServerClosed on graceful close
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
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
	client tunnel.TunnelClient,
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
	client tunnel.TunnelClient,
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
	client tunnel.TunnelClient,
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
	client tunnel.TunnelClient,
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
	client tunnel.TunnelClient,
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
