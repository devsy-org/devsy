package kubernetes

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/streaming/pkg/httpstream"
)

type Client struct {
	client *kubernetes.Clientset

	config *rest.Config
}

// NewClient constructs a struct wrapping the kubernetes client that is used by the kubernetes driver.
func NewClient(kubeConfig, kubeContext string) (*Client, string, error) {
	if kubeConfig == "" {
		kubeConfig = os.Getenv("KUBECONFIG")
	}

	// create client config loading rules
	var clientConfigLoadingRules *clientcmd.ClientConfigLoadingRules
	if kubeConfig != "" {
		clientConfigLoadingRules = &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeConfig}
	} else {
		clientConfigLoadingRules = clientcmd.NewDefaultClientConfigLoadingRules()
	}

	// load kubernetes config
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientConfigLoadingRules,
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	)

	clientConfig, err := config.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("failed to load kubernetes config: %w", err)
	}

	namespace, _, err := config.Namespace()
	if err != nil {
		return nil, "", fmt.Errorf("failed to load kubernetes namespace from config: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return nil, "", err
	}

	return &Client{
		client: kubeClient,
		config: clientConfig,
	}, namespace, nil
}

func (c *Client) Client() *kubernetes.Clientset {
	return c.client
}

// Ping reports whether the API server is reachable via its /version endpoint.
func (c *Client) Ping(ctx context.Context) error {
	return c.client.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Error()
}

func (c *Client) Config() *rest.Config {
	return c.config
}

func (c *Client) FullLogs(ctx context.Context, namespace, pod, container string) ([]byte, error) {
	logs, err := c.Logs(ctx, namespace, pod, container, true)
	if err != nil {
		return nil, err
	}

	return io.ReadAll(logs)
}

func (c *Client) Logs(
	ctx context.Context,
	namespace, pod, container string,
	follow bool,
) (io.ReadCloser, error) {
	return c.client.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
		Container: container,
		Follow:    follow,
	}).Stream(ctx)
}

type ExecStreamOptions struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Pod       string
	Namespace string
	Container string
	Command   []string
}

// Exec executes a kubectl exec with given transport round tripper and upgrader.
func (c *Client) Exec(ctx context.Context, options *ExecStreamOptions) error {
	client, err := kubernetes.NewForConfig(c.config)
	if err != nil {
		return err
	}

	execRequest := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(options.Pod).
		Namespace(options.Namespace).
		SubResource(string("exec")).
		VersionedParams(&corev1.PodExecOptions{
			Container: options.Container,
			Command:   options.Command,
			Stdin:     options.Stdin != nil,
			Stdout:    options.Stdout != nil,
			Stderr:    options.Stderr != nil,
		}, scheme.ParameterCodec)

	exec, err := newFallbackExecutor(c.config, execRequest.URL())
	if err != nil {
		return err
	}

	return waitForStream(ctx, func(streamCtx context.Context) error {
		return exec.StreamWithContext(streamCtx, remotecommand.StreamOptions{
			Stdin:  options.Stdin,
			Stdout: options.Stdout,
			Stderr: options.Stderr,
		})
	})
}

// waitForStream runs stream in a goroutine and waits for either its
// completion or ctx cancellation. stream is expected to observe ctx and
// return promptly once it's done, so this always waits for it -- never
// leaking the goroutine -- but never reports a cancelled or timed-out
// attempt as success by discarding ctx's own error.
func waitForStream(ctx context.Context, stream func(context.Context) error) error {
	errChan := make(chan error, 1)
	go func() {
		errChan <- stream(ctx)
	}()

	select {
	case <-ctx.Done():
		if streamErr := <-errChan; streamErr != nil {
			return streamErr
		}
		return ctx.Err()
	case err := <-errChan:
		if err == nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
		}
		return err
	}
}

// newFallbackExecutor prefers the WebSocket transport and falls back to SPDY,
// which is deprecated and disabled on newer API servers.
func newFallbackExecutor(config *rest.Config, url *url.URL) (remotecommand.Executor, error) {
	spdyExec, err := remotecommand.NewSPDYExecutor(config, "POST", url)
	if err != nil {
		return nil, err
	}

	wsExec, err := remotecommand.NewWebSocketExecutor(config, "GET", url.String())
	if err != nil {
		return nil, err
	}

	return remotecommand.NewFallbackExecutor(wsExec, spdyExec, httpstream.IsUpgradeFailure)
}
