package clierr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh/knownhosts"
)

type fakeSSHExit struct{ code int }

func (e fakeSSHExit) Error() string { return "remote process exited" }
func (e fakeSSHExit) ExitCode() int { return e.code }

func TestClassifySSHConnection(t *testing.T) {
	cause := errors.New("connection detail")
	cases := []struct {
		name string
		err  error
		code Code
	}{
		{"network", &net.OpError{Op: "dial", Net: "tcp", Err: cause}, CodeSSHUnreachable},
		{"host key", &knownhosts.KeyError{}, CodeSSHHostKeyFailed},
		{"revoked key", &knownhosts.RevokedError{}, CodeSSHHostKeyFailed},
		{
			"authentication",
			fmt.Errorf(
				"ssh: handshake failed: %w",
				errors.New(
					"ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain",
				),
			),
			CodeSSHAuthFailed,
		},
		{"untrusted text", errors.New("remote says unable to authenticate"), CodeSSHCommandFailed},
		{"other handshake", cause, CodeSSHCommandFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ClassifySSHConnection(tc.err)
			var classified *CLIError
			require.ErrorAs(t, err, &classified)
			require.Equal(t, tc.code, classified.Code)
			require.NotEmpty(t, classified.Hint)
			require.ErrorIs(t, err, tc.err)
		})
	}
}

func TestClassifySSHConnectionPreservesPriorErrors(t *testing.T) {
	existing := &CLIError{Code: CodeRateLimited, Message: "existing"}
	for _, err := range []error{
		nil,
		context.Canceled,
		fmt.Errorf("deadline: %w", context.DeadlineExceeded),
		existing,
		fmt.Errorf("wrapped: %w", existing),
		fakeSSHExit{code: 7},
		fakeSSHExit{code: 255},
	} {
		require.Equal(t, err, ClassifySSHConnection(err))
	}
}
