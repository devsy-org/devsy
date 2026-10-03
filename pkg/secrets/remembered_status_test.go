package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const rememberedStatusFixture = "remembered-status-fixture"

func TestRememberedStatusSurvivesUnknownOwnership(t *testing.T) {
	for _, test := range []struct {
		name       string
		value      string
		err        error
		remembered bool
		available  bool
	}{
		{"remembered", rememberedStatusFixture, nil, true, true},
		{"accessible empty", "", nil, false, true},
		{"unavailable", "", ErrBackendUnavailable, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, _ := newLegacyResetFixture(t, KindSecret, true)
			calls := 0
			p.readRemembered = func() (string, error) { calls++; return test.value, test.err }
			status, err := p.Status()
			require.NoError(t, err)
			require.Equal(t, test.remembered, status.Remembered)
			require.Equal(t, test.available, status.RememberedAvailable)
			require.Equal(t, SecretStateUnknown, status.Availability)
			require.Equal(t, "ownership_unknown", status.ReasonCode)
			require.Equal(t, 1, calls)
			catalog, err := p.CatalogStatus()
			require.NoError(t, err)
			require.False(t, catalog.Remembered)
			require.False(t, catalog.RememberedAvailable)
			require.Equal(t, "ownership_unknown", catalog.ReasonCode)
			_, err = p.ResetFileStore()
			require.ErrorIs(t, err, ErrStateIndeterminate)
			require.Equal(t, 1, calls, "catalog and refused reset must remain keyring-free")
		})
	}
}

func TestRememberedStatusSurvivesFileInspectionError(t *testing.T) {
	p, _ := newLegacyResetFixture(t, KindSecret, true)
	blob := filepath.Join(p.dir, EncryptedFileName)
	require.NoError(t, os.Remove(blob))
	// A symlink loop reliably produces an inspection error without requiring
	// permission changes that may behave differently for privileged test users.
	if err := os.Symlink(EncryptedFileName, blob); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	calls := 0
	p.readRemembered = func() (string, error) { calls++; return rememberedStatusFixture, nil }
	status, err := p.Status()
	require.NoError(t, err)
	require.True(t, status.Remembered)
	require.True(t, status.RememberedAvailable)
	require.Equal(t, SecretBackendUnavailable, status.Availability)
	require.Equal(t, "backend_unavailable", status.ReasonCode)
	_, err = p.CatalogStatus()
	require.NoError(t, err)
	require.Equal(t, 1, calls, "metadata-only status must not inspect the credential service")
}
