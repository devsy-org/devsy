//go:build !windows && !darwin

package command

// StartedSessionIdentity captures a child's tree identity before its parent
// reaps it. The caller must have started the child with Setsid enabled.
func StartedSessionIdentity(pid int) (string, error) {
	return processTreeIdentity(pid)
}
