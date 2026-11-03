package task

import "os/exec"

func startupTestChild() *exec.Cmd {
	return exec.Command("ping", "-n", "30", "127.0.0.1")
}
