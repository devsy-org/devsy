package command

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"

	"github.com/devsy-org/devsy/internal/diagnosticredact"
)

func WrapCommandError(stdout []byte, err error) error {
	if err == nil {
		return nil
	}

	return &Error{
		stdout: stdout,
		err:    err,
	}
}

func WrapCommandErrorWithEnvironment(stdout []byte, err error, environment []string) error {
	if err == nil {
		return nil
	}
	wrapped := &Error{stdout: stdout, err: err}
	masked := diagnosticredact.RecordWithEnvironment(environment, wrapped.output())
	wrapped.maskedOutput = &masked
	return wrapped
}

type Error struct {
	stdout       []byte
	err          error
	maskedOutput *string
}

func (e *Error) Error() string {
	if e.maskedOutput != nil {
		return *e.maskedOutput
	}
	return diagnosticredact.EnvironmentRecord(e.output())
}

func (e *Error) Unwrap() error {
	return e.err
}

func MatchErrorOutput(err error, match func(string) bool) bool {
	return err != nil && (match(err.Error()) || matchOriginalCommandOutput(err, match))
}

func matchOriginalCommandOutput(err error, match func(string) bool) bool {
	switch err := err.(type) {
	case nil:
		return false
	case *Error:
		return match(err.output()) || matchOriginalCommandOutput(err.err, match)
	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(err.Unwrap(), func(cause error) bool {
			return matchOriginalCommandOutput(cause, match)
		})
	default:
		return matchOriginalCommandOutput(errors.Unwrap(err), match)
	}
}

func (e *Error) output() string {
	message := ""
	if len(e.stdout) > 0 {
		message += string(e.stdout) + "\n"
	}

	var exitError *exec.ExitError
	if errors.As(e.err, &exitError) && len(exitError.Stderr) > 0 {
		message += string(exitError.Stderr) + "\n"
	}

	return message + e.err.Error()
}

func formatTaskkillDiagnostic(pid int, err error, output []byte) error {
	detail := truncateOutput(diagnosticredact.EnvironmentRecord(string(output)))
	return fmt.Errorf("taskkill /PID %d /T /F: %w: %s", pid, err, detail)
}

func truncateOutput(output string) string {
	const maxOutput = 256
	if len(output) > maxOutput {
		return output[:maxOutput] + "..."
	}
	return output
}

func Exists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func ExistsForUser(cmd, user string) bool {
	command := "which " + cmd
	var err error
	if user == "" {
		return Exists(cmd)
	}

	su := exec.Command("su", user, "-l", "-c", command) //nolint:gosec // fixed binary
	_, err = su.CombinedOutput()
	return err == nil
}
