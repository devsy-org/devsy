package external

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RuntimeError preserves canonical and runtime error categories without keeping
// secret-bearing backend diagnostics in a caller-visible error.
type RuntimeError struct {
	Operation string
	Code      codes.Code
	Category  runtimev1.RuntimeErrorCode
	Retryable bool
	Message   string
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("external runtime %s: %s", e.Operation, e.Message)
}

func (e *RuntimeError) GRPCStatus() *status.Status { return status.New(e.Code, e.Message) }

func (h *Host) operationError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("external runtime %s: %w", operation, ctx.Err())
	}
	rpcStatus, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("external runtime %s: %s", operation, h.redactor.Redact(err.Error()))
	}
	result := &RuntimeError{
		Operation: operation, Code: rpcStatus.Code(),
		Message: h.redactor.Redact(rpcStatus.Message()),
	}
	for _, detail := range rpcStatus.Details() {
		runtimeError, ok := detail.(*runtimev1.RuntimeError)
		if !ok {
			continue
		}
		if runtimeError.Message != "" {
			result.Message = h.redactor.Redact(runtimeError.Message)
		}
		result.Category, result.Retryable = runtimeError.Code, runtimeError.Retryable
		if runtimeError.RuntimeMessage != "" {
			log.Debugf(
				"External runtime %s diagnostic: %s",
				operation,
				h.redactor.Redact(runtimeError.RuntimeMessage),
			)
		}
		break
	}
	return result
}
