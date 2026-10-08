package microsandbox

import (
	"testing"

	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/require"
)

func TestBindMountValidation(t *testing.T) {
	for _, m := range []*devcontainerconfig.Mount{
		nil,
		{Source: "", Target: ""},
		{Source: "", Target: testBindDst},
		{Source: testBindSrc, Target: ""},
	} {
		require.Nil(t, bindMount(m))
	}
}

func TestBindMountConversion(t *testing.T) {
	rw := bindMount(&devcontainerconfig.Mount{Source: testBindSrc, Target: testBindDst})
	require.Equal(t, &volumeMount{Source: testBindSrc, Target: testBindDst, ReadOnly: false}, rw)

	ro := bindMount(
		&devcontainerconfig.Mount{Source: testBindSrc, Target: testBindDst, Other: []string{"ro"}},
	)
	require.Equal(t, &volumeMount{Source: testBindSrc, Target: testBindDst, ReadOnly: true}, ro)
}
