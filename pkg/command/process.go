package command

func IsRunning(pid string) (bool, error) {
	return isRunning(pid)
}

func Kill(pid string) error {
	return killTree(pid, "")
}

// KillTree terminates the process and everything it spawned: the job or
// process group treeName identifies when set, otherwise the process alone.
func KillTree(pid, treeName string) error {
	return killTree(pid, treeName)
}
