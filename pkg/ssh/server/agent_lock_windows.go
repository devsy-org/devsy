//go:build windows

package server

// takeAgentDirLock is a no-op on Windows; the agent-forwarding code path
// is only exercised by the unix-socket-based SSH server, so a stale-dir
// detection mechanism is not needed here.
func takeAgentDirLock(string) error { return nil }

// agentDirIsStale is a no-op on Windows; see takeAgentDirLock.
func agentDirIsStale(string) bool { return false }
