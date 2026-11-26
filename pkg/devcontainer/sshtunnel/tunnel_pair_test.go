package sshtunnel

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func runPairWith(
	t *testing.T,
	agentFn func(handlerDone <-chan struct{}) error,
	serverFn func() (*config2.Result, error),
) (*config2.Result, error) {
	t.Helper()
	bridge, err := tunnel.NewPipeBridge()
	require.NoError(t, err)
	defer bridge.Close()

	handlerDone := make(chan struct{})
	return runTunnelPair(context.Background(), bridge, tunnelPairFuncs{
		agent: func(context.Context, *os.File, *os.File) error {
			return agentFn(handlerDone)
		},
		server: func(context.Context, io.WriteCloser, io.Reader) (*config2.Result, error) {
			defer close(handlerDone)
			return serverFn()
		},
	})
}

func TestRunTunnelPairNamesAgentExitAfterCleanHandler(t *testing.T) {
	agentErr := errors.New("run agent command failed: Process exited with status 128")

	result, err := runPairWith(t,
		func(handlerDone <-chan struct{}) error { <-handlerDone; return agentErr },
		func() (*config2.Result, error) { return &config2.Result{}, nil },
	)

	require.NoError(t, err, "the RunPair nil classification stays")
	require.NotNil(t, result)
	require.Equal(
		t,
		"agent command exited: run agent command failed: Process exited with status 128",
		result.AgentExit,
	)
}

func TestRunTunnelPairLeavesAgentExitEmptyForCleanShutdown(t *testing.T) {
	tests := map[string]error{
		"clean exit": nil,
		"cancelled":  context.Canceled,
		"eof":        io.EOF,
	}
	for name, agentErr := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := runPairWith(t,
				func(handlerDone <-chan struct{}) error { <-handlerDone; return agentErr },
				func() (*config2.Result, error) { return &config2.Result{}, nil },
			)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Empty(t, result.AgentExit)
		})
	}
}

func TestRunTunnelPairKeepsHandlerFailureAsError(t *testing.T) {
	handlerErr := errors.New("handler broke")
	agentErr := errors.New("exit status 1")

	result, err := runPairWith(t,
		func(handlerDone <-chan struct{}) error { <-handlerDone; return agentErr },
		func() (*config2.Result, error) { return &config2.Result{}, handlerErr },
	)

	require.ErrorIs(t, err, handlerErr)
	require.NotNil(t, result)
	require.Empty(t, result.AgentExit)
}
