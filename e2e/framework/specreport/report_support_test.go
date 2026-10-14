package specreport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	fixtureLabel      = "docker-internal-buildkit"
	validFixture      = "valid_4of4.json"
	noExecutedMessage = "no executed It spec"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	//nolint:gosec // G304: fixture names are constants of the calling test
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}
