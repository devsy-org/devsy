package delivery

import (
	"context"
	"os/exec"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/status"
)

func TestRunCapturedAssociatesActiveOperation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses sh")
	}
	var resultOperationID string
	err := status.Run(context.Background(), status.Nop(), status.Operation{Phase: status.PhaseInjectingAgent}, func(ctx context.Context) error {
		result, err := runCaptured(ctx, exec.CommandContext(ctx, "sh", "-c", "exit 0"))
		resultOperationID = result.OperationID
		return err
	})
	if err != nil {
		t.Fatalf("status.Run: %v", err)
	}
	if resultOperationID == "" {
		t.Fatal("captured result has no active operation ID")
	}
}
