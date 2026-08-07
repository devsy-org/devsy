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

// sendResultTimeout bounds the final SendResult call so it isn't tied to the
// job's own (possibly already-cancelled) context: a cancellation-driven
// failure is exactly the case where the host still needs to hear about it.
const sendResultTimeout = 5 * time.Second

// ReportResult runs fn and guarantees its outcome is sent over the tunnel
// via SendResult exactly once before returning, whether fn succeeds or
// fails. RunWithResult's ctx.Done() branch treats "cancelled with no result"
// as a failure, so every RunUpServer/RunSetupServer caller (up, build,
// setup) must funnel through here rather than each remembering to call
// SendResult on its own exit paths — a gap on any one of them silently
// reintroduces that ambiguity.
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

	// A context independent of the job's own ctx: if the job failed because
	// its context was cancelled, using that same context here would prevent
	// this final completion signal from ever reaching the host.
	sendCtx, cancel := context.WithTimeout(context.Background(), sendResultTimeout)
	defer cancel()

	message := &tunnel.Message{Message: string(out)}
	if _, err := tunnelClient.SendResult(sendCtx, message); err != nil {
		return fmt.Errorf("send result: %w", err)
	}
	return nil
}
