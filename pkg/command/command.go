package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/internal/diagnosticredact"
	"github.com/devsy-org/devsy/logging/redact"
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
	redactor := redact.New(redact.Options{Environment: environment})
	wrapped.setDisplay(redactor, diagnosticredact.Records(redactor, wrapped.stderr()))
	return wrapped
}

func WrapCombinedOutputError(output string, err error, environment []string) error {
	if err == nil || output == "" {
		return err
	}
	redactor := redact.New(redact.Options{Environment: environment})
	masked := strings.TrimSpace(
		diagnosticredact.Record(redactor, output),
	)
	if masked == "" {
		return err
	}
	return &Error{
		captured:      output,
		maskedCapture: masked,
		err:           err,
	}
}

type Error struct {
	stdout        []byte
	captured      string
	maskedCapture string
	maskedOutput  *string
	err           error
}

func (e *Error) Error() string {
	if e.maskedOutput != nil {
		return *e.maskedOutput
	}
	if e.captured != "" {
		return e.maskedCapture + ": " + e.err.Error()
	}
	redactor := redact.New(redact.Options{Environment: os.Environ()})
	return e.display(redactor, diagnosticredact.Records(redactor, e.stderr()))
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
	if e.captured != "" {
		return e.captured + ": " + e.err.Error()
	}
	return commandErrorOutput(string(e.stdout), e.stderr(), e.err.Error())
}

func (e *Error) stderr() string {
	if exit, ok := errors.AsType[*exec.ExitError](e.err); ok {
		return string(exit.Stderr)
	}
	return ""
}

func (e *Error) setDisplay(redactor *redact.Redactor, stderr string) {
	masked := e.display(redactor, stderr)
	e.maskedOutput = &masked
}

func (e *Error) display(redactor *redact.Redactor, stderr string) string {
	stdout := diagnosticredact.Records(redactor, string(e.stdout))
	cause := diagnosticredact.Records(redactor, e.err.Error())
	return diagnosticredact.Record(redactor, commandErrorOutput(stdout, stderr, cause))
}

func commandErrorOutput(stdout, stderr, cause string) string {
	message := ""
	if stdout != "" {
		message += stdout + "\n"
	}
	if stderr != "" {
		message += stderr + "\n"
	}
	return message + cause
}

func formatTaskkillDiagnostic(pid int, err error, output []byte) error {
	detail := truncateOutput(diagnosticredact.EnvironmentRecord(string(output)))
	return fmt.Errorf("taskkill /PID %d /T /F: %w: %s", pid, err, detail)
}

func truncateOutput(output string) string {
	const maxOutput = 256
	if len(output) > maxOutput {
		return string(completeUTF8Prefix([]byte(output[:maxOutput]))) + "..."
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
