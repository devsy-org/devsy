package provider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExportConfig_SnapshotRefRoundTrips(t *testing.T) {
	cfg := &ExportConfig{SnapshotRef: testProviderSnapRef}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	var got ExportConfig
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, cfg.SnapshotRef, got.SnapshotRef)
}
