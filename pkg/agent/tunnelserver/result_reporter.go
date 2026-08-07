package tunnelserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

const sendResultTimeout = 5 * time.Second

// ReportResult runs fn and guarantees its outcome is sent over the tunnel
// via SendResult exactly once before returning, whether fn succeeds or
// fails.
func ReportResult(
	ctx context.Context,
	tunnelClient tunnel.TunnelClient,
	fn func(ctx context.Context) (*config.Result, error),
) (*config.Result, error) {
	result, err := fn(ctx)

	toSend := result
	if toSend == nil {
		toSend = &config.Result{}
	}
	if err != nil && toSend.Error == "" {
		toSend.Error = err.Error()
	}

	if sendErr := sendResult(tunnelClient, toSend); sendErr != nil {
		if err != nil {
			log.Errorf("failed to forward result to host: %v", sendErr)
			return result, err
		}
		return result, sendErr
	}

	return result, err
}

func sendResult(tunnelClient tunnel.TunnelClient, result *config.Result) error {
	out, err := json.Marshal(result)
	if err != nil {
		return err
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), sendResultTimeout)
	defer cancel()

	message := &tunnel.Message{Message: string(out)}
	if _, err := tunnelClient.SendResult(sendCtx, message); err != nil {
		return fmt.Errorf("send result: %w", err)
	}
	return nil
}
