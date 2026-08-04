package cmdinternal

import (
	"fmt"
	"os"
	"strconv"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/sharedfile"
	"github.com/spf13/cobra"
)

// NewWidenSharedFileCmd returns a internal command that runs
// sharedfile.WidenIfNeeded. sharedfile.WidenWithSudoFallback re-execs this
// (via sudo) so the actual mode change still goes through WidenIfNeeded's
// open-with-O_NOFOLLOW-then-fchmod, even when it needs root — `sudo chmod
// <path>` has no way to refuse following a symlink at path, so re-execing
// into this process is what keeps the escalated path symlink-safe.
func NewWidenSharedFileCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:    "widen-shared-file <path> <mode>",
		Short:  "Widen a coordination file's permissions if needed",
		Args:   cobra.ExactArgs(2),
		Hidden: true,
		RunE: func(_ *cobra.Command, args []string) error {
			mode, err := strconv.ParseUint(args[1], 8, 32)
			if err != nil {
				return fmt.Errorf("parse mode %q: %w", args[1], err)
			}
			return sharedfile.WidenIfNeeded(args[0], os.FileMode(mode))
		},
	}
}
