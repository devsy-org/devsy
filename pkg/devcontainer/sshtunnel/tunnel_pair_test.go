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

// runPairWith holds the agent side until the pair context is cancelled. RunPair
// cancels it only after the handler side returned, so the agent always finishes
// after RunPair recorded the handler outcome.
func runPairWith(
	t *testing.T,
	agentFn func() error,
	serverFn func() (*config2.Result, error),
) (*config2.Result, error) {
	t.Helper()
	bridge, err := tunnel.NewPipeBridge()
	require.NoError(t, err)
	defer bridge.Close()

	return runTunnelPair(context.Background(), bridge, tunnelPairFuncs{
		agent: func(ctx context.Context, _ *os.File, _ *os.File) error {
			<-ctx.Done()
			return agentFn()
		},
		server: func(context.Context, io.WriteCloser, io.Reader) (*config2.Result, error) {
			return serverFn()
		},
	})
}

func TestRunTunnelPairNamesAgentExitAfterCleanHandler(t *testing.T) {
	agentErr := errors.New("run agent command failed: Process exited with status 128")

	result, err := runPairWith(t,
		func() error { return agentErr },
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
				func() error { return agentErr },
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
		func() error { return agentErr },
		func() (*config2.Result, error) { return &config2.Result{}, handlerErr },
	)

	require.ErrorIs(t, err, handlerErr)
	require.NotNil(t, result)
	require.Empty(t, result.AgentExit)
}

func TestRunTunnelPairKeepsNilResult(t *testing.T) {
	agentErr := errors.New("exit status 1")

	result, err := runPairWith(t,
		func() error { return agentErr },
		func() (*config2.Result, error) { return nil, nil },
	)

	require.NoError(t, err)
	require.Nil(t, result)
}
