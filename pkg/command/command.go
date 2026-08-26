package command

import (
	"errors"
	"os/exec"
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

type Error struct {
	stdout []byte
	err    error
}

func (e *Error) Error() string {
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

func (e *Error) Unwrap() error {
	return e.err
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

	_, err = exec.Command( // #nosec G204 -- runs the caller-provided command as the target workspace user
		"su",
		user,
		"-l",
		"-c",
		command,
	).
		CombinedOutput()
	return err == nil
}
