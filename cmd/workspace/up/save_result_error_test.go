package up

import (
	"errors"
	"testing"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/require"
)

func TestSaveResultErrorNamesAgentExit(t *testing.T) {
	saveErr := errors.New("workspace demo no longer exists")
	result := &config2.Result{AgentExit: "agent command exited: exit status 128"}

	err := saveResultError(result, saveErr)

	require.ErrorIs(t, err, saveErr)
	require.Equal(
		t,
		"agent command exited: exit status 128; save workspace result: workspace demo no longer exists",
		err.Error(),
	)
}

func TestSaveResultErrorWithoutAgentExit(t *testing.T) {
	saveErr := errors.New("disk full")

	for name, result := range map[string]*config2.Result{
		"nil result":   nil,
		"empty result": {},
	} {
		t.Run(name, func(t *testing.T) {
			err := saveResultError(result, saveErr)

			require.ErrorIs(t, err, saveErr)
			require.Equal(t, "save workspace result: disk full", err.Error())
		})
	}
}
