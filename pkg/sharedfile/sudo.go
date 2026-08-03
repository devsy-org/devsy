package sharedfile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/pkg/log"
)

// WidenWithSudoFallback behaves like WidenIfNeeded, but on EPERM (path
// exists at the wrong mode and this process doesn't own it) falls back to
// re-execing `<self> internal widen-shared-file` under a non-interactive
// sudo, so the escalated mode change still goes through WidenIfNeeded's
// O_NOFOLLOW open rather than a plain `sudo chmod <path>` — chmod(1) has no
// way to refuse following a symlink at its target path. The fallback's
// failure is logged, not returned: this is a best-effort repair, and the
// caller's own lock acquisition will surface the real permission error if
// the repair fails.
func WidenWithSudoFallback(ctx context.Context, path string, mode os.FileMode) error {
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

	execPath, err := os.Executable()
	if err != nil {
		log.Debugf("resolve self for sudo widen fallback (non-fatal): %v", err)
		return nil
	}

	// -n: fail immediately instead of prompting if sudo needs a password,
	// so a caller holding a timeout-bounded lock can't hang forever.
	//nolint:gosec // execPath is the current binary; path is a fixed coordination-file path
	cmd := exec.CommandContext(
		ctx, "sudo", "-n", execPath, "internal", "widen-shared-file",
		path, fmt.Sprintf("%04o", mode.Perm()),
	)
	if sudoErr := cmd.Run(); sudoErr != nil {
		log.Debugf("sudo widen-shared-file %s (non-fatal): %v", path, sudoErr)
	}
	return nil
}
