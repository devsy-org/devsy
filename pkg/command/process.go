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

// KillTreeWithIdentity terminates a worker tree using its saved platform identity.
func KillTreeWithIdentity(pid, treeName, identity string) error {
	return killTreeWithIdentity(pid, treeName, identity)
}

// KillTreeAfterWorkerExit terminates descendants using the worker's saved session identity.
func KillTreeAfterWorkerExit(pid, treeName, identity string) error {
	return killTreeAfterWorkerExit(pid, treeName, identity)
}

// ProcessTreeIdentity returns the kernel identity used to distinguish a reused process ID.
func ProcessTreeIdentity(pid int) (string, error) {
	return processTreeIdentity(pid)
}
