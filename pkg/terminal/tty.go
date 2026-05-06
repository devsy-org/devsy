package terminal

import (
	"io"
	"os"

	dockerterm "github.com/moby/term"
)

var (
	IsTerminalIn  = IsTerminal(os.Stdin)
	IsTerminalOut = IsTerminal(os.Stdout)
)

// IsTerminal returns whether the passed object is a terminal or not.
func IsTerminal(stdin io.Reader) bool {
	_, terminal := dockerterm.GetFdInfo(stdin)
	return terminal
}
