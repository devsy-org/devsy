//go:build unix

package kubernetes

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestImageConfigFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, syscall.Mkfifo(path, 0o600))
	d := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			ImageConfigFile: path,
			Architecture:    "amd64",
		},
	}
	result := make(chan error, 1)
	go func() { _, err := d.ImageDetails(driver.ImageDetailsRequest{ImageName: testImage}); result <- err }()
	select {
	case err := <-result:
		require.ErrorContains(t, err, "regular")
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO open blocked")
	}
}
