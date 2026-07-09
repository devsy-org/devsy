package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopLevelCommand(t *testing.T) {
	rootCmd, _ := BuildRoot()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"daemon stays in sandbox", []string{internalCommand, "daemon-local"}, internalCommand},
		{"nested internal command", []string{internalCommand, "ssh-server"}, internalCommand},
		{"workspace up routes to host", []string{cmdWorkspace, "up", "."}, cmdWorkspace},
		{"workspace ssh routes to host", []string{cmdWorkspace, "ssh", "my-ws"}, cmdWorkspace},
		{"provider list routes to host", []string{cmdProvider, cmdList}, cmdProvider},
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
