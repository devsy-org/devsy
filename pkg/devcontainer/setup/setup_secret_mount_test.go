package setup

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteSecretFilesRequiresTmpfsMount(t *testing.T) {
	mountErr := errors.New("required tmpfs mount is missing")
	originalValidator := validateSecretFileRuntime
	validateSecretFileRuntime = func(string) error { return mountErr }
	t.Cleanup(func() { validateSecretFileRuntime = originalValidator })

	err := writeSecretFiles(&ContainerSetupConfig{
		SecretsMount: []string{"FILE_SECRET=sentinel-value"},
	})
	require.ErrorIs(t, err, mountErr)
}
