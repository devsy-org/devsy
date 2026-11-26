package clierr

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

const (
	unlockRequiredHint = "Supply DEVSY_SECRETS_PASSPHRASE, DEVSY_SECRETS_PASSPHRASE_FILE, " +
		"a remembered credential, or run interactively."
	rateLimitedMessage = "Rate limited by an upstream API. " +
		"Wait and retry, or authenticate for a higher limit."
	dockerDaemonHint = "Start the Docker daemon for the selected context and retry."
)

func TestClassifyCategories(t *testing.T) {
	buildErr := Recoverable(errors.New("build image: boom"))

	for _, tc := range []struct {
		name string
		err  error
		want CLIError
	}{
		{
			name: "recoverable build failure keeps the full message",
			err:  buildErr,
			want: CLIError{Code: CodeBuildFailedRecoverable, Message: "build image: boom"},
		},
		{
			name: "rate limited",
			err:  ErrRateLimited,
			want: CLIError{Code: CodeRateLimited, Message: rateLimitedMessage},
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: CLIError{
				Code:    CodeCanceled,
				Message: "Operation canceled.",
				Hint:    "Retry the operation when ready.",
			},
		},
		{
			name: "deadline exceeded",
			err:  context.DeadlineExceeded,
			want: CLIError{
				Code:    CodeDeadlineExceeded,
				Message: "Operation timed out.",
				Hint:    "Retry the operation or increase its timeout.",
			},
		},
		{
			name: "docker daemon unreachable",
			err:  errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock"),
			want: CLIError{
				Code:    CodeDockerDaemonUnreachable,
				Message: dockerDaemonUnavailableMessage,
				Hint:    dockerDaemonHint,
			},
		},
		{
			name: "unknown keeps the original message",
			err:  errors.New("kaboom"),
			want: CLIError{Code: CodeUnknown, Message: "kaboom"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireClassified(t, tc.err, tc.want)
		})
	}
}

func TestClassifySecretCategories(t *testing.T) {
	secretNotFound := fmt.Errorf("lookup TOKEN: %w", secrets.ErrSecretNotFound)

	for _, tc := range []struct {
		name string
		err  error
		want CLIError
	}{
		{
			name: "unlock required",
			err:  &secrets.UnlockRequiredError{Backend: secrets.BackendFile},
			want: CLIError{
				Code:    CodeUnlockRequired,
				Message: (&secrets.UnlockRequiredError{}).Error(),
				Hint:    unlockRequiredHint,
			},
		},
		{
			name: "unlock failed",
			err:  &secrets.UnlockFailedError{Cause: errors.New("credential sentinel")},
			want: CLIError{
				Code:    CodeUnlockFailed,
				Message: (&secrets.UnlockFailedError{}).Error(),
				Hint:    "Verify the passphrase and encrypted store, then retry.",
			},
		},
		{
			name: "secret backend unavailable",
			err:  &secrets.BackendUnavailableError{Cause: errors.New("credential sentinel")},
			want: CLIError{
				Code:    CodeSecretBackendUnavailable,
				Message: (&secrets.BackendUnavailableError{}).Error(),
				Hint:    "Restore access to the secret backend and retry.",
			},
		},
		{
			name: "secret store corrupt",
			err:  &secrets.StoreCorruptError{Cause: errors.New("plaintext sentinel")},
			want: CLIError{
				Code:    CodeSecretStoreCorrupt,
				Message: (&secrets.StoreCorruptError{}).Error(),
				Hint:    "Restore a valid encrypted store backup.",
			},
		},
		{
			name: "secret not found keeps the wrapping message",
			err:  secretNotFound,
			want: CLIError{
				Code:    CodeSecretNotFound,
				Message: secretNotFound.Error(),
				Hint:    "Create or repair the required secret and retry.",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireClassified(t, tc.err, tc.want)
		})
	}
}

func requireClassified(t *testing.T, err error, want CLIError) {
	t.Helper()

	got := Classify(err)

	require.NotNil(t, got)
	require.Equal(t, want.Code, got.Code)
	require.Equal(t, want.Message, got.Message)
	require.Equal(t, want.Hint, got.Hint)
	require.Nil(t, got.Context)
	require.Equal(t, err, got.Unwrap())
}

func TestClassifyWrappedSentinelsKeepClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code Code
	}{
		{"recoverable build", Recoverable(errors.New("boom")), CodeBuildFailedRecoverable},
		{"rate limited", ErrRateLimited, CodeRateLimited},
		{"canceled", context.Canceled, CodeCanceled},
		{"deadline exceeded", context.DeadlineExceeded, CodeDeadlineExceeded},
		{"secret not found", secrets.ErrSecretNotFound, CodeSecretNotFound},
		{"unlock required", secrets.ErrUnlockRequired, CodeUnlockRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", tc.err))

			got := Classify(wrapped)

			require.Equal(t, tc.code, got.Code)
			require.ErrorIs(t, got, tc.err)
		})
	}
}

func TestClassifyPrecedence(t *testing.T) {
	existing := &CLIError{Code: CodeRateLimited, Message: "existing"}

	for _, tc := range []struct {
		name string
		err  error
		code Code
	}{
		{
			name: "secret error beats recoverable build failure",
			err:  errors.Join(Recoverable(errors.New("boom")), secrets.ErrUnlockFailed),
			code: CodeUnlockFailed,
		},
		{
			name: "recoverable build failure beats rate limit",
			err:  errors.Join(ErrRateLimited, Recoverable(errors.New("boom"))),
			code: CodeBuildFailedRecoverable,
		},
		{
			name: "rate limit beats cancellation",
			err:  errors.Join(context.Canceled, ErrRateLimited),
			code: CodeRateLimited,
		},
		{
			name: "cancellation beats deadline",
			err:  errors.Join(context.DeadlineExceeded, context.Canceled),
			code: CodeCanceled,
		},
		{
			name: "deadline beats docker daemon message",
			err: fmt.Errorf(
				"cannot connect to the docker daemon: %w",
				context.DeadlineExceeded,
			),
			code: CodeDeadlineExceeded,
		},
		{
			name: "secret sentinel beats docker daemon message",
			err: fmt.Errorf(
				"is the docker daemon running: %w",
				secrets.ErrBackendUnavailable,
			),
			code: CodeSecretBackendUnavailable,
		},
		{
			name: "existing CLIError beats every sentinel",
			err:  fmt.Errorf("outer: %w", errors.Join(existing, secrets.ErrUnlockRequired)),
			code: CodeRateLimited,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.code, Classify(tc.err).Code)
		})
	}
}

func TestClassifyReturnsExistingCLIErrorUnchanged(t *testing.T) {
	existing := &CLIError{
		Code:    CodeUnknown,
		Message: "cannot connect to the docker daemon",
		Context: map[string]string{"k": "v"},
	}

	got := Classify(fmt.Errorf("outer: %w", existing))

	require.Same(t, existing, got)
}

func TestClassifyTypedNilCLIErrorFallsThroughToUnknown(t *testing.T) {
	var typedNil *CLIError

	got := Classify(typedNil)

	require.NotNil(t, got)
	require.NotSame(t, typedNil, got)
	require.Equal(t, CodeUnknown, got.Code)
	require.Empty(t, got.Message)
}

func TestClassifyDockerDaemonMessageMatching(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		code    Code
	}{
		{"connect phrase", "cannot connect to the docker daemon", CodeDockerDaemonUnreachable},
		{"running phrase", "is the docker daemon running?", CodeDockerDaemonUnreachable},
		{"upper case", "CANNOT CONNECT TO THE DOCKER DAEMON", CodeDockerDaemonUnreachable},
		{"embedded in prefix and suffix", "x: Is The Docker Daemon Running? y", CodeDockerDaemonUnreachable},
		{"partial connect phrase", "cannot connect to the docker", CodeUnknown},
		{"partial running phrase", "is the docker running", CodeUnknown},
		{"unrelated docker text", "docker build failed", CodeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(errors.New(tc.message))

			require.Equal(t, tc.code, got.Code)
			if tc.code == CodeUnknown {
				require.Equal(t, tc.message, got.Message)
			}
		})
	}
}

func TestClassifySecretMessagesDoNotLeakCause(t *testing.T) {
	for _, err := range []error{
		&secrets.UnlockFailedError{Cause: errors.New("credential sentinel")},
		&secrets.BackendUnavailableError{Cause: errors.New("credential sentinel")},
		&secrets.StoreCorruptError{Cause: errors.New("credential sentinel")},
	} {
		got := Classify(fmt.Errorf("required TOKEN: %w", err))

		require.NotContains(t, got.Message, "sentinel")
		require.NotContains(t, got.Hint, "sentinel")
	}
}
