package env

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

const isolationTestEnvValue = "debug"

func TestEnvironmentCommandsRemainUsableWithCorruptSecretCatalog(t *testing.T) {
	flags := setupEnvCommandTest(t)
	flags.ResultFormat = "plain"
	path, err := config.GetConfigPath()
	require.NoError(t, err)
	secretPath := filepath.Join(filepath.Dir(path), "secrets.yaml")
	require.NoError(t, os.WriteFile(secretPath, []byte("contexts: [broken"), 0o600))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(filepath.Dir(path), "secrets.enc"), []byte("corrupt"), 0o600),
	)
	t.Setenv("DEVSY_SECRETS_PASSPHRASE", "")
	t.Setenv("DEVSY_SECRETS_PASSPHRASE_FILE", "/nonexistent")
	require.NoError(
		t,
		(&SetCmd{GlobalFlags: flags, Value: isolationTestEnvValue}).Run(
			context.Background(),
			"LOG_LEVEL",
		),
	)
	require.NoError(t, (&GetCmd{GlobalFlags: flags}).Run(context.Background(), "LOG_LEVEL"))
	require.NoError(t, (&ListCmd{GlobalFlags: flags}).Run(context.Background()))
	require.NoError(t, (&AttachCmd{GlobalFlags: flags}).Run(context.Background(), "LOG_LEVEL"))
	require.NoError(t, (&DetachCmd{GlobalFlags: flags}).Run(context.Background(), "LOG_LEVEL"))
	require.NoError(t, (&DeleteCmd{GlobalFlags: flags}).Run(context.Background(), "LOG_LEVEL"))
	raw, err := os.ReadFile(secretPath) // #nosec G304 -- selected temporary config fixture.
	require.NoError(t, err)
	require.Equal(t, "contexts: [broken", string(raw))
}
