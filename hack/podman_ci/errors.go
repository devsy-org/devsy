package main

import (
	"errors"
	"fmt"
)

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

func usageError(format string, args ...any) error {
	return &ExitError{Code: 2, Err: fmt.Errorf(format, args...)}
}

func classifySetupError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*ExitError](err); ok {
		return err
	}
	return &OperationalError{Err: err}
}

type OperationalError struct{ Err error }

func (e *OperationalError) Error() string { return e.Err.Error() }
func (e *OperationalError) Unwrap() error { return e.Err }

func exitCodeForError(err error) int {
	if exitErr, ok := errors.AsType[*ExitError](err); ok {
		return exitErr.Code
	}
	if _, ok := errors.AsType[*OperationalError](err); ok {
		return 1
	}
	return 2
}
