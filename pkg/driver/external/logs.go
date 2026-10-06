package external

import (
	"context"
	"errors"
	"io"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
)

// GetDevContainerLogs writes the protocol's merged binary stream to stdout.
// Stderr is unused because Runtime Protocol v1 Logs has no channel identity.
func (h *Host) GetDevContainerLogs(
	ctx context.Context,
	workspaceID string,
	stdout, _ io.Writer,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if workspaceID == "" {
		return errors.New("external runtime Logs requires workspace ID")
	}
	if !h.info.Capabilities.Logs {
		return &RuntimeError{
			Operation: "Logs", Code: codes.Unimplemented,
			Category: runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNSUPPORTED,
			Message:  "runtime does not support Logs",
		}
	}
	if stdout == nil {
		stdout = io.Discard
	}
	return h.forWorkspace(workspaceID, nil).
		call(ctx, "Logs", func(client runtimev1.RuntimeDriverClient) error {
			stream, err := client.Logs(ctx, &runtimev1.LogsRequest{WorkspaceId: workspaceID})
			if err != nil {
				return err
			}
			return receiveLogs(stream, stdout)
		})
}

func receiveLogs(stream runtimev1.RuntimeDriver_LogsClient, stdout io.Writer) error {
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := writeOutputChunk(stdout, chunk); err != nil {
			return err
		}
	}
}
