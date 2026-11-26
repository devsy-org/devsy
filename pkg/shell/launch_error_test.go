package shell

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithLaunchHintLeavesOtherErrorsUnchanged(t *testing.T) {
	cause := &fs.PathError{Op: "fork/exec", Path: "provider", Err: fs.ErrPermission}

	require.Same(t, cause, WithLaunchHint(cause))
	require.NoError(t, WithLaunchHint(nil))
	require.NotContains(t, WithLaunchHint(errors.New("boom")).Error(), "ERROR_INVALID_PARAMETER")
}
