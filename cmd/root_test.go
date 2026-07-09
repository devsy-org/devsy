package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTopLevelCommand verifies the classifier that gates the Flatpak host
// re-exec: the daemon lives under `internal`, which must stay in-sandbox, while
// user-facing commands like `up`/`ssh` must be routed to the host.
func TestTopLevelCommand(t *testing.T) {
	rootCmd, _ := BuildRoot()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"daemon stays in sandbox", []string{internalCommand, "daemon-local"}, internalCommand},
		{"nested internal command", []string{internalCommand, "ssh-server"}, internalCommand},
		{"workspace up routes to host", []string{"workspace", "up", "."}, "workspace"},
		{"workspace ssh routes to host", []string{"workspace", "ssh", "my-ws"}, "workspace"},
		{"provider list routes to host", []string{"provider", "list"}, "provider"},
		{"bare root", []string{}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found, _, err := rootCmd.Find(tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, topLevelCommand(found))
		})
	}
}
