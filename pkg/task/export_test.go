package task

// SetAfterClaimForTest runs fn inside Reconcile, just after it claims the dead
// worker's lock.
func (s *Store) SetAfterClaimForTest(fn func()) {
	s.afterClaimForTest = fn
}

// SetKillProcessForTest replaces this store's process termination hook, so a
// test can drive cancellation outcomes deterministically without real PIDs.
func (s *Store) SetKillProcessForTest(fn func(string) error) {
	s.killProcess = fn
}
