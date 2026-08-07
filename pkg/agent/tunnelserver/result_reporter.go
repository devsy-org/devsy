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
	result, err := runJob(ctx, fn)

	toSend := &config.Result{}
	if result != nil {
		copied := *result
		toSend = &copied
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

// runJob recovers a panic in fn so ReportResult's completion guarantee holds
// even when the job crashes outright, instead of the host waiting forever
// for a result that will never arrive.
func runJob(
	ctx context.Context,
	fn func(ctx context.Context) (*config.Result, error),
) (result *config.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn(ctx)
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
