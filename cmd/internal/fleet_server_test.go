package cmdinternal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fleetAuthorized   = "12:00 Received authorization request from client\n"
	fleetConnected    = "12:01 Notify abc is connected\n"
	fleetDisconnected = "12:02 Notify abc is disconnected\n"
)

func writeFleetLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet.log")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestFleetSessionActive(t *testing.T) {
	tests := []struct {
		name string
		log  string
		want bool
	}{
		{name: "connected after authorization", log: fleetAuthorized + fleetConnected, want: true},
		{
			name: "disconnected last",
			log:  fleetAuthorized + fleetConnected + fleetDisconnected,
		},
		{
			name: "reconnected after a disconnect",
			log:  fleetAuthorized + fleetConnected + fleetDisconnected + fleetConnected,
			want: true,
		},
		{name: "connected line before any authorization is ignored", log: fleetConnected},
		{name: "authorized but no status line yet", log: fleetAuthorized},
		{name: "empty log", log: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, fleetSessionActive(writeFleetLog(t, tt.log)))
		})
	}
}

func TestFleetSessionActiveMissingLogIsInactive(t *testing.T) {
	assert.False(t, fleetSessionActive(filepath.Join(t.TempDir(), "absent.log")))
}
