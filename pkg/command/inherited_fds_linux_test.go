package command

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const inheritedFDsHelperEnv = "DEVSY_TEST_INHERITED_FDS_MODE"

func TestMarkInheritedFDsCloseOnExec(t *testing.T) {
	for _, mode := range []string{"close_range", "proc_fallback", "close_range_fails"} {
		t.Run(mode, func(t *testing.T) {
			leaked, err := os.Open(os.DevNull)
			require.NoError(t, err)
			t.Cleanup(func() { _ = leaked.Close() })

			child := exec.Command(
				os.Args[0],
				"-test.run=^TestInheritedFDsHelper$",
			) // #nosec G204 G702 -- re-exec this test binary
			child.Env = append(os.Environ(), inheritedFDsHelperEnv+"="+mode)
			child.ExtraFiles = []*os.File{leaked}
			child.Stderr = os.Stderr
			output, err := child.Output()
			require.NoError(t, err, string(output))
		})
	}
}

func TestInheritedFDsHelper(t *testing.T) {
	mode := os.Getenv(inheritedFDsHelperEnv)
	if mode == "" {
		return
	}
	const leakedFD = 3

	flags, err := unix.FcntlInt(leakedFD, unix.F_GETFD, 0)
	require.NoError(t, err)
	require.Zero(t, flags&unix.FD_CLOEXEC)

	switch mode {
	case "proc_fallback":
		markProcFDsCloseOnExec()
	case "close_range_fails":
		closeRange = func(uint, uint, uint) error { return unix.EPERM }
		MarkInheritedFDsCloseOnExec()
	default:
		MarkInheritedFDsCloseOnExec()
	}

	flags, err = unix.FcntlInt(leakedFD, unix.F_GETFD, 0)
	require.NoError(t, err)
	require.NotZero(t, flags&unix.FD_CLOEXEC)

	grandchild := exec.Command("sh", "-c", "test ! -e /proc/$$/fd/3")
	require.NoError(t, grandchild.Run())
}
