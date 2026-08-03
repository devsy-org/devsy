package sharedfile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
)

// WidenWithSudoFallback behaves like WidenIfNeeded, but on EPERM (the file
// exists at the wrong mode and this process doesn't own it — e.g. a stale
// file a prior root process created before this package's fix, or a
// same-container root/remoteUser pair racing setup) falls back to a
// non-interactive `sudo chmod`. The fallback's failure is logged via logFn
// rather than returned: callers hold a lock/flock that will itself
// surface the real permission error to whichever caller actually needs to
// read or write the file next, so this is a best-effort repair, not a hard
// requirement for the caller's own success.
func WidenWithSudoFallback(
	ctx context.Context,
	path string,
	mode os.FileMode,
	logFn func(format string, args ...any),
) error {
	err := WidenIfNeeded(path, mode)
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}

	// -n: fail immediately instead of prompting if sudo needs a password,
	// so a caller holding a timeout-bounded lock can't hang forever.
	//nolint:gosec // path is a fixed coordination-file path, not user input
	cmd := exec.CommandContext(ctx, "sudo", "-n", "chmod", fmt.Sprintf("%04o", mode.Perm()), path)
	if sudoErr := cmd.Run(); sudoErr != nil {
		logFn("sudo chmod %s (non-fatal): %v", path, sudoErr)
	}
	return nil
}
