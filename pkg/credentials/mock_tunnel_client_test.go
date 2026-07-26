package credentials

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"google.golang.org/grpc"
)

type mockCredentialsClient struct {
	gitSSHSignatureFunc func(ctx context.Context, msg *tunnel.Message) (*tunnel.Message, error)
}

func (m *mockCredentialsClient) GitSSHSignature(
	ctx context.Context,
	in *tunnel.Message,
	opts ...grpc.CallOption,
) (*tunnel.Message, error) {
	return m.gitSSHSignatureFunc(ctx, in)
}

func (m *mockCredentialsClient) GitCredentials(
	ctx context.Context,
	in *tunnel.Message,
	opts ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockCredentialsClient) DockerCredentials(
	ctx context.Context,
	in *tunnel.Message,
	opts ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockCredentialsClient) GPGPublicKeys(
	ctx context.Context,
	in *tunnel.Message,
	opts ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockCredentialsClient) DevsyConfig(
	ctx context.Context,
	in *tunnel.Message,
	opts ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}
