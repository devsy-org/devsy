// Package clierr classifies CLI errors into stable codes.
package clierr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"
)

type Code string

const (
	dockerDaemonUnavailableMessage      = "Docker daemon is unavailable."
	CodeRateLimited                Code = "RATE_LIMITED"
	CodePanic                      Code = "PANIC"
	CodeUnknown                    Code = "UNKNOWN"
	CodeBuildFailedRecoverable     Code = "BUILD_FAILED_RECOVERABLE"
	CodeDockerDaemonUnreachable    Code = "docker_daemon_unreachable"
	CodeCanceled                   Code = "canceled"
	CodeDeadlineExceeded           Code = "deadline_exceeded"
)

type CLIError struct {
	Code    Code              `json:"code"`
	Message string            `json:"message"`
	Hint    string            `json:"hint,omitempty"`
	Context map[string]string `json:"context,omitempty"`

	wrapped error
}

func NewPanic(recovered any) *CLIError {
	return &CLIError{
		Code:    CodePanic,
		Message: fmt.Sprintf("internal error: %v", recovered),
		wrapped: fmt.Errorf("panic: %v", recovered),
	}
}

func (e *CLIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.wrapped
}

func (e *CLIError) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Code    Code              `json:"code"`
		Message string            `json:"message"`
		Hint    string            `json:"hint,omitempty"`
		Context map[string]string `json:"context,omitempty"`
	}{Code: e.Code, Message: e.Message, Hint: e.Hint, Context: e.Context})
}

func (e *CLIError) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	if e == nil {
		return nil
	}
	enc.AddString("code", string(e.Code))
	enc.AddString("message", e.Message)
	if e.Hint != "" {
		enc.AddString("hint", e.Hint)
	}
	if len(e.Context) > 0 {
		enc.AddString("context", fmt.Sprint(e.Context))
	}
	return nil
}

var ErrRateLimited = errors.New("rate limited")

var ErrBuildFailedRecoverable = errors.New("dev container build failed")

type recoverableBuildError struct{ err error }

func (e recoverableBuildError) Error() string { return e.err.Error() }
func (e recoverableBuildError) Unwrap() error { return e.err }
func (e recoverableBuildError) Is(target error) bool {
	return target == ErrBuildFailedRecoverable
}

func Recoverable(err error) error {
	if err == nil {
		return nil
	}
	return recoverableBuildError{err: err}
}

func Classify(err error) *CLIError {
	if err == nil {
		return nil
	}

	var cliErr *CLIError
	if errors.As(err, &cliErr) && cliErr != nil {
		return cliErr
	}

	if errors.Is(err, ErrBuildFailedRecoverable) {
		return &CLIError{
			Code:    CodeBuildFailedRecoverable,
			Message: err.Error(),
			wrapped: err,
		}
	}

	if errors.Is(err, ErrRateLimited) {
		return &CLIError{
			Code:    CodeRateLimited,
			Message: "Rate limited by an upstream API. Wait and retry, or authenticate for a higher limit.",
			wrapped: err,
		}
	}

	if errors.Is(err, context.Canceled) {
		return &CLIError{
			Code:    CodeCanceled,
			Message: "Operation canceled.",
			Hint:    "Retry the operation when ready.",
			wrapped: err,
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &CLIError{
			Code:    CodeDeadlineExceeded,
			Message: "Operation timed out.",
			Hint:    "Retry the operation or increase its timeout.",
			wrapped: err,
		}
	}

	message := err.Error()
	lowerMessage := strings.ToLower(message)
	if strings.Contains(lowerMessage, "cannot connect to the docker daemon") ||
		strings.Contains(lowerMessage, "is the docker daemon running") {
		return &CLIError{
			Code:    CodeDockerDaemonUnreachable,
			Message: dockerDaemonUnavailableMessage,
			Hint:    "Start the Docker daemon for the selected context and retry.",
			wrapped: err,
		}
	}

	return &CLIError{Code: CodeUnknown, Message: message, wrapped: err}
}
