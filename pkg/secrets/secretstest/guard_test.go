package secretstest

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	rootCommand  = "github.com/devsy-org/devsy"
	fixturePath  = "github.com/devsy-org/devsy/pkg/secrets/secretstest"
	overridePath = "github.com/devsy-org/devsy/pkg/secrets/internal/scryptcost"
)

func dependencies(t *testing.T, target string) []string {
	t.Helper()
	// #nosec G204 -- Fixed go list arguments over this module's own packages.
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", target).CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.Fields(string(out))
}

func TestCommandDoesNotLinkFixtureWorkFactor(t *testing.T) {
	deps := dependencies(t, rootCommand)
	require.Contains(t, deps, overridePath, "the production secrets path owns the override hook")
	require.NotContains(t, deps, fixturePath, "the command must not link the fixture work factor")
}
