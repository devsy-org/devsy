package sharedfile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
)

// WidenWithSudoFallback behaves like WidenIfNeeded, but on EPERM (path
// exists at the wrong mode and this process doesn't own it) falls back to a
// non-interactive `sudo chmod`. The fallback's failure is logged via logFn,
// not returned: this is a best-effort repair, and the caller's own lock
// acquisition will surface the real permission error if the repair fails.
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
