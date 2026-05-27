package setup

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/log"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
)

// fakeTunnelClient is a minimal stub of tunnel.TunnelClient. Only KubeConfig
// is exercised here; the other methods return zero values and are unused by
// setupKubeConfig.
type fakeTunnelClient struct {
	tunnel.TunnelClient // embed for the unused methods (nil panics if called)
	kubeConfigPayload   string
}

// Compile-time guard: a typo in the KubeConfig signature would silently
// fail to override the embedded interface method otherwise.
var _ tunnel.TunnelClient = (*fakeTunnelClient)(nil)

func (f *fakeTunnelClient) KubeConfig(
	_ context.Context, _ *tunnel.Message, _ ...grpc.CallOption,
) (*tunnel.Message, error) {
	return &tunnel.Message{Message: f.kubeConfigPayload}, nil
}

// TestSetupKubeConfig_EmptyPayloadSuppressesInfoLog verifies that an empty
// KubeConfig RPC reply does NOT emit the "setup KubeConfig" Info line. The
// e2e substring-absence check is brittle to log renames; this is the
// direct guard for the demotion to Debug.
func TestSetupKubeConfig_EmptyPayloadSuppressesInfoLog(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)

	client := &fakeTunnelClient{kubeConfigPayload: ""}
	if err := setupKubeConfig(context.Background(), nil, client); err != nil {
		t.Fatalf("setupKubeConfig: %v", err)
	}

	for _, entry := range logs.All() {
		if entry.Level >= zapcore.InfoLevel {
			// "setup KubeConfig" specifically must not appear at Info+.
			if entry.Message == "setup KubeConfig" {
				t.Errorf("expected no 'setup KubeConfig' log; got: %+v", entry)
			}
		}
	}
	if got := logs.FilterMessageSnippet("setup KubeConfig").Len(); got != 0 {
		t.Errorf("expected zero 'setup KubeConfig' entries, got %d", got)
	}
}

// TestSetupKubeConfig_NonEmptyPayloadEmitsInfoLog asserts the Info-level
// "setup KubeConfig" line still fires when the host returns a non-empty
// kubeconfig payload. writeKubeConfig may fail in the test environment, but
// the log is emitted BEFORE that call so we still expect to see it.
func TestSetupKubeConfig_NonEmptyPayloadEmitsInfoLog(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)

	client := &fakeTunnelClient{
		kubeConfigPayload: "apiVersion: v1\nclusters: []",
	}
	// Error is expected because writeKubeConfig will fail without a valid
	// remote user / home dir in the test env; we only care about the log.
	_ = setupKubeConfig(context.Background(), nil, client)

	if got := logs.FilterMessage("setup KubeConfig").Len(); got == 0 {
		t.Errorf(
			"expected at least one 'setup KubeConfig' log entry on non-empty payload, got 0 (all=%v)",
			logs.All(),
		)
	}
}
