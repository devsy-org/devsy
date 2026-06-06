package provider

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestLoadProviderConfig_RefreshesInternalProvider verifies that a stored
// built-in provider config with a stale exec.command (e.g. the removed
// "helper sh" wrapper baked in before a CLI rename) is refreshed from the
// embedded provider definition on load.
func TestLoadProviderConfig_RefreshesInternalProvider(t *testing.T) {
	setupTestHome(t)

	stale := &ProviderConfig{
		Name:    DockerDriver,
		Version: "v0.0.0-stale",
		Source:  ProviderSource{Internal: true, Raw: DockerDriver},
		Exec: ProviderCommands{
			Command: []string{`"${DEVSY}" helper sh -c "${COMMAND}"`},
		},
	}
	require.NoError(t, SaveProviderConfig(config.DefaultContext, stale))

	loaded, err := LoadProviderConfig(config.DefaultContext, DockerDriver)
	require.NoError(t, err)
	require.Len(t, loaded.Exec.Command, 1)
	require.NotContains(t, loaded.Exec.Command[0], "helper sh",
		"internal provider must be refreshed from embedded yaml, not the stale stored config")
	require.True(t, strings.Contains(loaded.Exec.Command[0], "internal sh"),
		"refreshed command should use the current 'internal sh' wrapper")
	// The embedded YAML is the source of truth for internal providers, so the
	// whole definition (not just exec.command) is taken from embed: the stale
	// stored Version is replaced and Source.Internal stays set.
	require.NotEqual(t, "v0.0.0-stale", loaded.Version,
		"internal refresh replaces the stored definition wholesale, including Version")
	require.True(t, loaded.Source.Internal, "refreshed provider must remain internal")
}

// TestLoadProviderConfig_UnknownInternalFallsBack ensures an internal provider
// whose name is not a built-in (e.g. a removed/renamed provider lingering on
// disk) falls back to the stored config rather than failing or returning empty.
func TestLoadProviderConfig_UnknownInternalFallsBack(t *testing.T) {
	setupTestHome(t)

	stored := &ProviderConfig{
		Name:    "retired-provider",
		Version: "v0.0.1",
		Source:  ProviderSource{Internal: true, Raw: "retired-provider"},
		Exec: ProviderCommands{
			Command: []string{`"${DEVSY}" internal sh -c "${COMMAND}"`},
		},
	}
	require.NoError(t, SaveProviderConfig(config.DefaultContext, stored))

	loaded, err := LoadProviderConfig(config.DefaultContext, "retired-provider")
	require.NoError(t, err)
	require.Equal(t, stored.Exec.Command, loaded.Exec.Command)
	require.Equal(t, "v0.0.1", loaded.Version)
}

// TestLoadProviderConfig_PreservesExternalProvider ensures non-internal
// providers are loaded verbatim from disk (no embedded refresh).
func TestLoadProviderConfig_PreservesExternalProvider(t *testing.T) {
	setupTestHome(t)

	external := &ProviderConfig{
		Name:    DockerDriver,
		Version: "v0.0.1",
		Source:  ProviderSource{Github: "some-org/some-provider"},
		Exec: ProviderCommands{
			Command: []string{`"${DEVSY}" helper sh -c "${COMMAND}"`},
		},
	}
	require.NoError(t, SaveProviderConfig(config.DefaultContext, external))

	loaded, err := LoadProviderConfig(config.DefaultContext, DockerDriver)
	require.NoError(t, err)
	require.Equal(t, external.Exec.Command, loaded.Exec.Command)
}
