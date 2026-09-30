//go:build linux

package setup

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/moby/sys/mountinfo"
	"github.com/stretchr/testify/require"
)

func TestValidateSecretEnvironmentMountInfo(t *testing.T) {
	tests := []struct {
		name    string
		entries string
		wantErr bool
	}{
		{
			name:    "exact tmpfs mount",
			entries: "36 35 0:32 / " + config.SecretsEnvDir + " rw,nosuid,nodev - tmpfs tmpfs rw\n",
		},
		{
			name:    "path absent",
			entries: "36 35 0:32 / /run rw - tmpfs tmpfs rw\n",
			wantErr: true,
		},
		{
			name:    "exact bind mount",
			entries: "36 35 0:32 / " + config.SecretsEnvDir + " rw - ext4 /dev/root rw\n",
			wantErr: true,
		},
		{
			name:    "tmpfs parent is not dedicated mount",
			entries: "36 35 0:32 / /run rw - tmpfs tmpfs rw\n",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mounts, err := mountinfo.GetMountsFromReader(
				strings.NewReader(test.entries),
				mountinfo.SingleEntryFilter(config.SecretsEnvDir),
			)
			require.NoError(t, err)
			err = validateSecretEnvironmentMountInfo(mounts, config.SecretsEnvDir)
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
