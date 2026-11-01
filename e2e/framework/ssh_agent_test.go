package framework

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitForAgentSocket_ReturnsWhenSocketAppears(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "agent.sock")

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(sockPath, nil, 0o600)
	}()

	require.NoError(t, waitForAgentSocket(sockPath))
}

func TestWaitForAgentSocket_ErrorWhenSocketNeverAppears(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "missing.sock")

	require.Error(t, waitForAgentSocket(sockPath))
}
