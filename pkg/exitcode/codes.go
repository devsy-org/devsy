// Package exitcode defines the CLI's process exit codes.
package exitcode

const (
	Success = 0
	Failure = 1

	// Retryable marks a transient failure the caller may retry (sysexits EX_TEMPFAIL).
	Retryable = 75
)
