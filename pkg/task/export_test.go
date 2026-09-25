package task

// SetAfterClaimForTest runs fn inside Reconcile, just after it claims the dead
// worker's lock.
func (s *Store) SetAfterClaimForTest(fn func()) {
	s.afterClaimForTest = fn
}
