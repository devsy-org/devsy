package tunnel_test

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	agenttunnel "github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/stdio"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type bridgeResultSuite struct{ suite.Suite }

func TestBridgeResultSuite(t *testing.T) { suite.Run(t, new(bridgeResultSuite)) }

func (s *bridgeResultSuite) TestForwardedAgentErrorSurvivesSSHExit() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	bridge, err := tunnel.NewPipeBridge()
	s.Require().NoError(err)
	defer bridge.Close()
	var result *config2.Result
	err = bridge.RunPair(ctx,
		func(ctx context.Context, stdin, stdout *os.File) error {
			conn, err := grpc.NewClient(
				"passthrough:///agent",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(
					func(context.Context, string) (net.Conn, error) { return stdio.NewStdioStream(stdin, stdout), nil },
				),
			)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			_, err = tunnelserver.ReportResult(
				ctx,
				agenttunnel.NewTunnelClient(conn),
				func(context.Context) (*config2.Result, error) {
					return nil, errors.New("workspace changed; use --recreate")
				},
			)
			if err == nil {
				return errors.New("expected agent failure")
			}
			return errors.New("SSH command failed with exit code 1")
		},
		func(ctx context.Context, stdout, stdin *os.File) error {
			var handlerErr error
			result, handlerErr = tunnelserver.New().RunWithResult(ctx, stdout, stdin)
			<-ctx.Done()
			return handlerErr
		},
	)
	s.Require().NoError(err)
	s.Require().NotNil(result)
	s.EqualError(result.Err(), "workspace changed; use --recreate")
}
