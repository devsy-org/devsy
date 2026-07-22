package flags

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

// TestEnvName_MatchesConfigConstants pins the uniform rule against the
// canonical env-var constants in pkg/config. If a constant ever stops matching
// what EnvName(flagName) produces, this test fires so the two surfaces stay in
// sync.
func TestEnvName_MatchesConfigConstants(t *testing.T) {
	cases := map[string]string{
		"home":      config.EnvHome,
		"debug":     config.EnvDebug,
		"agent-url": config.EnvAgentURL,
		"log-level": config.EnvLogLevel,
	}
	for flagName, want := range cases {
		assert.Equal(t, want, EnvName(flagName), "EnvName(%q)", flagName)
	}
}
