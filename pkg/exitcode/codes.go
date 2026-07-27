// Package exitcode defines the CLI's process exit codes.
package exitcode

const (
	Success = 0
	Failure = 1

	// Retryable marks a transient failure the caller may retry (sysexits EX_TEMPFAIL).
	Retryable = 75

	// BuildFailedRecoverable marks a build failure retryable with --recovery.
	// 79 is just past the sysexits range (64–78).
	BuildFailedRecoverable = 79
)
