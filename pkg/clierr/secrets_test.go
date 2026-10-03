package clierr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

func TestSecretErrorsHaveStableCodesAndSafeMessages(t *testing.T) {
	cases := []struct {
		err  error
		code Code
	}{
		{&secrets.UnlockRequiredError{Backend: secrets.BackendFile}, CodeUnlockRequired},
		{
			&secrets.UnlockFailedError{
				Backend: secrets.BackendFile,
				Cause:   errors.New("credential sentinel"),
			},
			CodeUnlockFailed,
		},
		{
			&secrets.BackendUnavailableError{
				Backend: secrets.BackendKeyring,
				Cause:   errors.New("credential sentinel"),
			},
			CodeSecretBackendUnavailable,
		},
		{
			&secrets.StoreCorruptError{Cause: errors.New("plaintext sentinel")},
			CodeSecretStoreCorrupt,
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.code), func(t *testing.T) {
			classified := Classify(fmt.Errorf("required TOKEN: %w", tc.err))
			require.Equal(t, tc.code, classified.Code)
			require.NotContains(t, classified.Message, "sentinel")
			require.ErrorIs(t, classified, tc.err)
		})
	}
}
