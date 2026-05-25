// Package errors provides structured CLI error classification.
//
// CLIError is the single typed error surface returned from the CLI to its
// callers (terminal users and the desktop app over IPC). Classify inspects a
// raw Go error and any captured sub-process stderr against a fingerprint
// table (see patterns.go) and produces a CLIError with a user-facing message,
// an actionable hint, and a documentation link.
//
// The JSON shape of CLIError — and in particular the field names code,
// message, hint, docUrl, provider, and cause — is the IPC contract with the
// desktop app and MUST NOT change without coordinated changes in both repos.
package errors

import (
	"encoding/json"

	"go.uber.org/zap/zapcore"
)

// Code is the stable, machine-readable identifier for a class of CLI errors.
// The string values are part of the IPC contract.
type Code string

const (
	CodeAWSProfileMissing Code = "AWS_PROFILE_MISSING"
	//nolint:gosec // G101 false positive: this is an error-code identifier, not a credential.
	CodeAWSCredsInvalid    Code = "AWS_CREDS_INVALID"
	CodeAWSRegionMissing   Code = "AWS_REGION_MISSING"
	CodeDockerNotRunning   Code = "DOCKER_NOT_RUNNING"
	CodeDockerPermDenied   Code = "DOCKER_PERMISSION_DENIED"
	CodeKubeConfigMissing  Code = "KUBE_CONFIG_MISSING"
	CodeKubeUnreachable    Code = "KUBE_UNREACHABLE"
	CodePodmanSocket       Code = "PODMAN_SOCKET_UNAVAILABLE"
	CodeProviderInitFailed Code = "PROVIDER_INIT_FAILED"
	CodeUnknown            Code = "UNKNOWN"
)

// CLIError is a structured error suitable for both terminal rendering and
// JSON-IPC transport to the desktop app.
type CLIError struct {
	Code     Code   `json:"code"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
	DocURL   string `json:"docUrl,omitempty"`
	Provider string `json:"provider,omitempty"`
	Cause    string `json:"cause,omitempty"`

	// wrapped preserves the original error chain for errors.Is/As.
	wrapped error `json:"-"`
}

// Error returns the user-facing one-line summary.
func (e *CLIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap returns the original error so errors.Is / errors.As continue to work.
func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.wrapped
}

// MarshalJSON ensures stable field ordering and matches the IPC contract.
func (e *CLIError) MarshalJSON() ([]byte, error) {
	type wire struct {
		Code     Code   `json:"code"`
		Message  string `json:"message"`
		Hint     string `json:"hint,omitempty"`
		DocURL   string `json:"docUrl,omitempty"`
		Provider string `json:"provider,omitempty"`
		Cause    string `json:"cause,omitempty"`
	}
	return json.Marshal(wire{
		Code:     e.Code,
		Message:  e.Message,
		Hint:     e.Hint,
		DocURL:   e.DocURL,
		Provider: e.Provider,
		Cause:    e.Cause,
	})
}

// MarshalLogObject teaches zap to encode a *CLIError as a structured JSON
// object (instead of falling back to .Error()). Field names must stay in
// lock-step with MarshalJSON and the IPC contract documented at the top of
// this file.
func (e *CLIError) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	if e == nil {
		return nil
	}
	enc.AddString("code", string(e.Code))
	enc.AddString("message", e.Message)
	if e.Hint != "" {
		enc.AddString("hint", e.Hint)
	}
	if e.DocURL != "" {
		enc.AddString("docUrl", e.DocURL)
	}
	if e.Provider != "" {
		enc.AddString("provider", e.Provider)
	}
	if e.Cause != "" {
		enc.AddString("cause", e.Cause)
	}
	return nil
}

// ClassifyContext carries optional information that helps Classify produce a
// more specific CLIError. Both fields are optional.
type ClassifyContext struct {
	// Provider is the name of the provider the error originated from
	// (e.g. "aws", "docker"). Empty when not applicable.
	Provider string

	// Stderr is the captured stderr of a sub-process invocation, if any.
	// Many sub-binaries (provider plugins) exit with "exit status 1" while
	// the actionable detail lives in stderr.
	Stderr string
}

// Classify maps a Go error and optional sub-process stderr to a typed
// CLIError. It always returns a non-nil *CLIError when err is non-nil.
// Returns nil only when err is nil.
//
// The classifier walks the fingerprint table in declaration order; the first
// matching pattern wins. Unmatched errors fall through to CodeUnknown with
// the original error text as the message.
func Classify(err error, ctx ClassifyContext) *CLIError {
	if err == nil {
		return nil
	}
	if cliErr, ok := err.(*CLIError); ok {
		return cloneCLIErrorWithContext(cliErr, ctx)
	}

	cause := buildCause(err, ctx.Stderr)
	haystack := err.Error()
	if ctx.Stderr != "" {
		haystack = haystack + "\n" + ctx.Stderr
	}

	for _, p := range patterns {
		if p.matches(haystack) {
			return &CLIError{
				Code:     p.Code,
				Message:  p.Message,
				Hint:     p.Hint,
				DocURL:   p.DocURL,
				Provider: ctx.Provider,
				Cause:    cause,
				wrapped:  err,
			}
		}
	}

	// Provider-init catch-all: surface a generic provider-init error when a
	// Provider is set in the context but no specific fingerprint matched.
	if ctx.Provider != "" {
		return &CLIError{
			Code:     CodeProviderInitFailed,
			Message:  "Provider initialization failed.",
			Hint:     "Re-run with --debug for the original error, or check the provider configuration.",
			Provider: ctx.Provider,
			Cause:    cause,
			wrapped:  err,
		}
	}

	return &CLIError{
		Code:     CodeUnknown,
		Message:  err.Error(),
		Provider: ctx.Provider,
		Cause:    cause,
		wrapped:  err,
	}
}

// cloneCLIErrorWithContext returns a copy of cliErr with Provider/Cause
// filled in from ctx when missing. The input pointer is never mutated; this
// prevents callers' shared CLIError instances from drifting when wrapped at
// multiple layers.
func cloneCLIErrorWithContext(cliErr *CLIError, ctx ClassifyContext) *CLIError {
	clone := *cliErr
	if clone.Provider == "" {
		clone.Provider = ctx.Provider
	}
	if clone.Cause == "" {
		clone.Cause = buildCause(cliErr, ctx.Stderr)
	}
	return &clone
}

// buildCause assembles a cause string from the Go error and any captured
// stderr. Both are included because the Go error is usually opaque
// ("exit status 1") while the sub-process stderr carries the real detail.
func buildCause(err error, stderr string) string {
	if err == nil {
		return stderr
	}
	if stderr == "" {
		return err.Error()
	}
	return err.Error() + ": " + stderr
}
