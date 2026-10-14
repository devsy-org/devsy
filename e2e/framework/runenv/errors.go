package runenv

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Code is a stable, grep-able failure identity printed with every error.
type Code string

const (
	CodeConfigInvalid         Code = "CI_CONFIG_INVALID"
	CodeProfileUnsupported    Code = "CI_PROFILE_UNSUPPORTED"
	CodeDockerWSLDistroGone   Code = "CI_DOCKER_WSL_DISTRO_MISSING"
	CodeDockerWSLInstall      Code = "CI_DOCKER_WSL_INSTALL_FAILED"
	CodeDockerWSLDaemon       Code = "CI_DOCKER_WSL_DAEMON_NOT_READY"
	CodeDockerCLIChecksum     Code = "CI_DOCKER_CLI_CHECKSUM_MISMATCH"
	CodeDockerWSLSSHNotReady  Code = "CI_DOCKER_WSL_SSH_NOT_READY"
	CodeDockerWSLSSHAuth      Code = "CI_DOCKER_WSL_SSH_AUTH_FAILED"
	CodeDockerWSLEndpoint     Code = "CI_DOCKER_WSL_ENDPOINT_MISMATCH"
	CodeDockerWSLOracle       Code = "CI_DOCKER_WSL_ORACLE_INVALID"
	CodeDockerWSLSetupTimeout Code = "CI_DOCKER_WSL_SETUP_TIMEOUT"
	CodeDockerWSLCleanup      Code = "CI_DOCKER_WSL_CLEANUP_FAILED"
	CodeE2EPreflightFailed    Code = "CI_E2E_PREFLIGHT_FAILED"
	CodeE2ETestFailed         Code = "CI_E2E_TEST_FAILED"
	CodeE2ETestTimeout        Code = "CI_E2E_TEST_TIMEOUT"
	CodeE2EReportInvalid      Code = "CI_E2E_REPORT_INVALID"
)

const (
	ExitCodeFailure = 1
	ExitCodeUsage   = 2

	// maxDetailBytes bounds the redacted child-output tail embedded in an error.
	MaxDetailBytes = 4 << 10
)

// StageError is a typed, stage-scoped failure. Fields other than Code, Stage
// and Err are optional and rendered only when set.
type StageError struct {
	Code     Code
	Stage    string
	Elapsed  time.Duration
	ExitCode *int
	Artifact string
	Detail   string // already redacted and bounded
	Err      error
}

func NewStageError(code Code, stage string, err error) *StageError {
	return &StageError{Code: code, Stage: stage, Err: err}
}

func ConfigError(format string, args ...any) *StageError {
	return NewStageError(CodeConfigInvalid, "config", fmt.Errorf(format, args...))
}

func UnsupportedProfileError(format string, args ...any) *StageError {
	return NewStageError(CodeProfileUnsupported, "config", fmt.Errorf(format, args...))
}

func (e *StageError) WithElapsed(d time.Duration) *StageError {
	e.Elapsed = d
	return e
}

func (e *StageError) WithExitCode(code int) *StageError {
	e.ExitCode = &code
	return e
}

func (e *StageError) WithArtifact(path string) *StageError {
	e.Artifact = path
	return e
}

// WithDetail attaches a redacted, tail-bounded excerpt of child output.
func (e *StageError) WithDetail(output string) *StageError {
	e.Detail = TailBytes(Redact(strings.TrimSpace(output)), MaxDetailBytes)
	return e
}

func (e *StageError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: stage=%s", e.Code, e.Stage)
	if e.Elapsed > 0 {
		fmt.Fprintf(&b, " elapsed=%s", e.Elapsed.Round(time.Millisecond))
	}
	if e.ExitCode != nil {
		fmt.Fprintf(&b, " exit=%d", *e.ExitCode)
	}
	if e.Artifact != "" {
		fmt.Fprintf(&b, " artifact=%s", e.Artifact)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(Redact(e.Err.Error()))
	}
	if e.Detail != "" {
		b.WriteString("\n")
		b.WriteString(e.Detail)
	}
	return b.String()
}

func (e *StageError) Unwrap() error { return e.Err }

// exitCodeForError maps an error to the process exit status. Untyped errors
// come from Cobra argument parsing and are usage errors.
func ExitCodeForError(err error) int {
	if err == nil {
		return 0
	}
	if se, ok := errors.AsType[*StageError](err); ok {
		switch se.Code {
		case CodeConfigInvalid, CodeProfileUnsupported:
			return ExitCodeUsage
		default:
			return ExitCodeFailure
		}
	}
	return ExitCodeUsage
}

// tailBytes returns at most max trailing bytes of s on a valid UTF-8 boundary.
func TailBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return "[truncated] " + strings.ToValidUTF8(s[len(s)-limit:], "")
}
