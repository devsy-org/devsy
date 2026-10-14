package e2erun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Shared literals of the runner tests.
const (
	buildkitLabel = "docker-internal-buildkit"
	fixtureValid4 = "valid_4of4.json"
	msgNoExecuted = "no executed It spec"
	cmdPodman     = "podman"
	envGHUser     = "GH_USERNAME=octo"
)

// readFixture loads a Ginkgo report from the specreport package's fixtures.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	//nolint:gosec // G304: fixture names are constants of the calling test
	data, err := os.ReadFile(filepath.Join("..", "specreport", "testdata", name))
	require.NoError(t, err)
	return data
}
