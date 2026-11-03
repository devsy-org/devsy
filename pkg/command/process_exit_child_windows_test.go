package command

import "os/exec"

func processExitTestChild() *exec.Cmd {
	return exec.Command("ping", "-n", "30", "127.0.0.1")
}
