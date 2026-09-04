package secrets

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testSOPSPlaintextMarker = "SUPER_SECRET_TEST_VALUE_7B91"
	testSOPSFixturePath     = "testdata/sops-age.yaml"
	// Synthetic test-only identity generated solely for this fixture. gitleaks:allow.
	testSOPSAgeIdentity = "AGE-SECRET-KEY-12UWYSAH2MRDQ5K4EWC4253PDTCSCS32Y5EFQ8TEN2SL3QYU2GN2SG88CZX"
)

func encryptedSOPSTestFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(testSOPSFixturePath)
	require.NoError(t, err)
	return data
}

func TestSOPSSourceDecryptsAgeFixture(t *testing.T) {
	t.Setenv("SOPS_AGE_KEY", testSOPSAgeIdentity)
	source := NewSOPSDataSource(
		"test",
		"secrets.enc.yaml",
		SOPSFormatYAML,
		encryptedSOPSTestFixture(t),
	)

	secret, err := source.Get(context.Background(), "SOPS_E2E_SECRET")
	require.NoError(t, err)
	require.Equal(t, testSOPSPlaintextMarker, secret.Value)
	require.True(t, secret.Sensitive)

	mounted, err := source.Get(context.Background(), "TLS_KEY")
	require.NoError(t, err)
	require.Equal(t, "mounted-value-77", mounted.Value)
}

func TestSOPSSourceDecryptFailureDoesNotLeakPlaintext(t *testing.T) {
	t.Setenv("SOPS_AGE_KEY", "")
	t.Setenv("SOPS_AGE_KEY_FILE", t.TempDir()+"/missing")
	source := NewSOPSDataSource(
		"test",
		"secrets.enc.yaml",
		SOPSFormatYAML,
		encryptedSOPSTestFixture(t),
	)

	err := source.Validate(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), testSOPSPlaintextMarker)
	require.NotContains(t, strings.ToLower(err.Error()), "mounted-value-77")
}

func TestParseSOPSDocumentYAML(t *testing.T) {
	values, err := parseSOPSDocument(
		[]byte("API_TOKEN: secret\nPORT: 5432\nDEBUG: false\n"),
		SOPSFormatYAML,
	)
	require.NoError(t, err)
	require.Equal(t, "secret", values["API_TOKEN"])
	require.Equal(t, "5432", values["PORT"])
	require.Equal(t, "false", values["DEBUG"])
}

func TestParseSOPSDocumentJSON(t *testing.T) {
	values, err := parseSOPSDocument(
		[]byte(`{"API_TOKEN":"secret","PORT":5432}`),
		SOPSFormatJSON,
	)
	require.NoError(t, err)
	require.Equal(t, "secret", values["API_TOKEN"])
	require.Equal(t, "5432", values["PORT"])
}

func TestParseSOPSDocumentJSONPreservesLargeIntegers(t *testing.T) {
	values, err := parseSOPSDocument(
		[]byte(`{"API_TOKEN":"secret","COUNT":9007199254740993}`),
		SOPSFormatJSON,
	)
	require.NoError(t, err)
	require.Equal(t, "secret", values["API_TOKEN"])
	require.Equal(t, "9007199254740993", values["COUNT"])
}

func TestParseSOPSDocumentDotenv(t *testing.T) {
	values, err := parseSOPSDocument(
		[]byte("API_TOKEN=secret\nPORT=5432\n"),
		SOPSFormatDotenv,
	)
	require.NoError(t, err)
	require.Equal(t, "secret", values["API_TOKEN"])
	require.Equal(t, "5432", values["PORT"])
}

func TestParseSOPSDocumentRejectsNestedValue(t *testing.T) {
	_, err := parseSOPSDocument(
		[]byte("database:\n  password: secret\n"),
		SOPSFormatYAML,
	)
	require.ErrorContains(t, err, "not a scalar")
}

func TestNormalizeSOPSFormat(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"secrets.enc.yaml", SOPSFormatYAML},
		{"secrets.enc.yml", SOPSFormatYAML},
		{"secrets.enc.json", SOPSFormatJSON},
		{"secrets.enc.env", SOPSFormatDotenv},
	} {
		got, err := normalizeSOPSFormat("", tc.path)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}
