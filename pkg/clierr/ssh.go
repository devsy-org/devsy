package clierr

import (
	"context"
	"errors"
	"net"
	"strings"

	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	CodeSSHAuthFailed    Code = "ssh_auth_failed"
	CodeSSHHostKeyFailed Code = "ssh_host_key_failed"
	CodeSSHUnreachable   Code = "ssh_unreachable"
	CodeSSHCommandFailed Code = "ssh_command_failed"
)

type sshExitCoder interface {
	ExitCode() int
}

func ClassifySSHConnection(err error) error {
	if preserveSSHError(err) {
		return err
	}
	var keyErr *knownhosts.KeyError
	var revokedErr *knownhosts.RevokedError
	var netErr *net.OpError
	var code Code
	var message, hint string
	switch {
	case errors.As(err, &keyErr), errors.As(err, &revokedErr):
		code = CodeSSHHostKeyFailed
		message = "SSH host key verification failed."
		hint = "Verify the server identity and update the trusted host key."
	case errors.As(err, &netErr):
		code = CodeSSHUnreachable
		message = "SSH could not reach the remote host."
		hint = "Check the host name, network, port, and SSH service."
	case isSSHAuthenticationFailure(err):
		code = CodeSSHAuthFailed
		message = "SSH authentication failed."
		hint = "Check the SSH user, key, and server authorization."
	default:
		code = CodeSSHCommandFailed
		message = "SSH connection failed."
		hint = "Check the SSH configuration and connection."
	}
	return &CLIError{Code: code, Message: message, Hint: hint, wrapped: err}
}

func preserveSSHError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	_, ok := errors.AsType[*CLIError](err)
	return ok || isSSHRemoteExit(err)
}

func isSSHRemoteExit(err error) bool {
	var exitCode sshExitCoder
	var exitStatus interface{ ExitStatus() int }
	return errors.As(err, &exitCode) || errors.As(err, &exitStatus)
}

func isSSHAuthenticationFailure(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		text := current.Error()
		if strings.HasPrefix(text, "ssh: unable to authenticate, attempted methods [") &&
			strings.HasSuffix(text, "], no supported methods remain") {
			return true
		}
	}
	return false
}
