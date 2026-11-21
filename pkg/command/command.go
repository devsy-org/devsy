package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	wrapped.setDisplay(redactor, maskJSONRecords(redactor, wrapped.stderr()))
	return wrapped
}

func WrapCombinedOutputError(output string, err error, environment []string) error {
	if err == nil || output == "" {
		return err
	}
	redactor := redact.New(redact.Options{Environment: environment})
	masked := strings.TrimSpace(
		diagnosticredact.Record(redactor, maskJSONRecords(redactor, output)),
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
	return e.display(redactor, maskJSONRecords(redactor, e.stderr()))
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
	stdout := maskJSONRecords(redactor, string(e.stdout))
	cause := maskJSONRecords(redactor, e.err.Error())
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

func maskJSONRecords(redactor *redact.Redactor, value string) string {
	if json.Valid([]byte(value)) {
		return diagnosticredact.Record(redactor, value)
	}
	var output strings.Builder
	for value != "" {
		if looksLikeStructuredJSON(value) {
			value = value[maskStructuredRecord(redactor, value, &output):]
			continue
		}
		line, rest, newline := strings.Cut(value, "\n")
		output.WriteString(maskJSONLine(redactor, line))
		if newline {
			output.WriteByte('\n')
		}
		value = rest
	}
	return output.String()
}

func looksLikeStructuredJSON(value string) bool {
	value = strings.TrimLeft(value, " \t\r")
	if value == "" {
		return false
	}
	rest := strings.TrimLeft(value[1:], " \t\r\n")
	switch value[0] {
	case '{':
		return rest == "" || rest[0] == '"' || rest[0] == '}'
	case '[':
		return looksLikeJSONList(rest)
	default:
		return false
	}
}

func looksLikeJSONList(rest string) bool {
	if rest == "" || strings.ContainsRune(`{[]"`, rune(rest[0])) {
		return true
	}
	if !strings.ContainsRune(`-0123456789tfn`, rune(rest[0])) {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(rest))
	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		return errors.Is(err, io.ErrUnexpectedEOF)
	}
	tail := strings.TrimLeft(rest[int(decoder.InputOffset()):], " \t\r\n")
	return tail == "" || tail[0] == ',' || tail[0] == ']'
}

func maskStructuredRecord(redactor *redact.Redactor, value string, output *strings.Builder) int {
	decoder := json.NewDecoder(strings.NewReader(value))
	var record json.RawMessage
	if err := decoder.Decode(&record); err != nil {
		output.WriteString(redactor.Mask())
		return len(value)
	}
	output.WriteString(diagnosticredact.Record(redactor, string(record)))
	return int(decoder.InputOffset())
}

func maskJSONLine(redactor *redact.Redactor, line string) string {
	if json.Valid([]byte(line)) {
		body, cr := strings.CutSuffix(line, "\r")
		masked := diagnosticredact.Record(redactor, body)
		if cr {
			return masked + "\r"
		}
		return masked
	}
	return line
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
