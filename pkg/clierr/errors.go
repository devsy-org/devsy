// Package clierr classifies CLI errors into stable codes.
package clierr

import (
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap/zapcore"
)

type Code string

const (
	CodeRateLimited Code = "RATE_LIMITED"
	CodePanic       Code = "PANIC"
	CodeUnknown     Code = "UNKNOWN"
)

type CLIError struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`

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
		Code    Code   `json:"code"`
		Message string `json:"message"`
	}{Code: e.Code, Message: e.Message})
}

func (e *CLIError) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	if e == nil {
		return nil
	}
	enc.AddString("code", string(e.Code))
	enc.AddString("message", e.Message)
	return nil
}

var ErrRateLimited = errors.New("rate limited")

func Classify(err error) *CLIError {
	if err == nil {
		return nil
	}

	var cliErr *CLIError
	if errors.As(err, &cliErr) && cliErr != nil {
		return cliErr
	}

	if errors.Is(err, ErrRateLimited) {
		return &CLIError{
			Code:    CodeRateLimited,
			Message: "Rate limited by an upstream API. Wait and retry, or authenticate for a higher limit.",
			wrapped: err,
		}
	}

	return &CLIError{Code: CodeUnknown, Message: err.Error(), wrapped: err}
}
