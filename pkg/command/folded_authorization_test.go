package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	foldedToken       = "synthetic-folded-token"
	foldedPublic      = "public detail"
	foldedChildEnv    = "DEVSY_TEST_FOLDED_CHILD"
	foldedChildStdout = "DEVSY_TEST_FOLDED_STDOUT"
	foldedChildStderr = "DEVSY_TEST_FOLDED_STDERR"
	foldedChildArg    = "-test.run=^TestFoldedAuthorizationChild$"
)

var foldedJSONString = "\"Authorization:\"\n Bearer " + foldedToken + "\n\n" + foldedPublic

type foldedCause struct {
	text string
	exit *exec.ExitError
}

func (e *foldedCause) Error() string { return e.text }

func (e *foldedCause) Unwrap() error { return e.exit }

func TestFoldedAuthorizationChild(t *testing.T) {
	if os.Getenv(foldedChildEnv) == "" {
		t.Skip("helper process")
	}
	_, _ = fmt.Fprint(os.Stdout, os.Getenv(foldedChildStdout))
	_, _ = fmt.Fprint(os.Stderr, os.Getenv(foldedChildStderr))
	os.Exit(3)
}

func foldedChild(t *testing.T, stdout, stderr string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], foldedChildArg) //nolint:gosec // re-executes this test binary
	cmd.Env = append(
		os.Environ(),
		foldedChildEnv+"=1",
		foldedChildStdout+"="+stdout,
		foldedChildStderr+"="+stderr,
	)
	return cmd
}

func TestWrapCommandErrorMasksFoldedAuthorizationAfterJSONString(t *testing.T) {
	cause := errors.New("fixture failure")
	wrapped := WrapCommandErrorWithEnvironment([]byte(foldedJSONString), cause, nil)
	require.ErrorIs(t, wrapped, cause)
	assert.NotContains(t, wrapped.Error(), foldedToken)
	assert.Contains(t, wrapped.Error(), foldedPublic)
}

func TestWrapCommandErrorMasksFoldedAuthorizationInEveryComponent(t *testing.T) {
	cases := map[string]error{
		"stderr": &exec.ExitError{Stderr: []byte(foldedJSONString)},
		"cause":  errors.New(foldedJSONString),
	}
	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			wrapped := WrapCommandErrorWithEnvironment(nil, cause, nil)
			assert.NotContains(t, wrapped.Error(), foldedToken)
			assert.Contains(t, wrapped.Error(), foldedPublic)
		})
	}
}

func TestOutputMasksFoldedAuthorizationAfterJSONString(t *testing.T) {
	for name, streams := range map[string][2]string{
		"stderr": {"", foldedJSONString},
		"stdout": {foldedJSONString, ""},
		"split":  {"\"Authorization:\"", " Bearer " + foldedToken + "\n\n" + foldedPublic},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Output(foldedChild(t, streams[0], streams[1]), IncludeFailureStdout)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), foldedToken)
			assert.Contains(t, err.Error(), foldedPublic)
		})
	}
}

func TestTaskkillDiagnosticMasksFoldedAuthorizationAfterJSONString(t *testing.T) {
	err := formatTaskkillDiagnostic(43, errors.New("fixture failure"), []byte(foldedJSONString))
	assert.NotContains(t, err.Error(), foldedToken)
	assert.Contains(t, err.Error(), foldedPublic)
}

func TestFoldedAuthorizationSplitsMatchTheDiagnosticStream(t *testing.T) {
	lines := []string{
		`"Authorization:"`, `{"a":"Authorization:"}`, `Authorization:`,
		" Bearer " + foldedToken, ``, `public`, `"Authorization: Bearer"`,
		`  {"k":"` + foldedToken + `"}`,
	}
	var checked int
	var walk func(prefix []string)
	walk = func(prefix []string) {
		if len(prefix) > 0 {
			checked += checkFoldedSplits(t, prefix)
		}
		if len(prefix) == 3 {
			return
		}
		for _, line := range lines {
			walk(append(append([]string{}, prefix...), line))
		}
	}
	walk(nil)
	assert.Positive(t, checked)
}

func checkFoldedSplits(t *testing.T, parts []string) int {
	t.Helper()
	checked := 0
	for i := 0; i <= len(parts); i++ {
		for j := i; j <= len(parts); j++ {
			stdout := strings.Join(parts[:i], "\n")
			stderr := strings.Join(parts[i:j], "\n")
			cause := strings.Join(parts[j:], "\n")
			joined := foldedJoin(stdout, stderr, cause)
			if !strings.Contains(joined, foldedToken) {
				continue
			}
			stream := redact.NewDiagnosticStream(redact.New(redact.Options{}))
			if strings.Contains(stream.RedactChunk(joined)+stream.Flush(), foldedToken) {
				continue
			}
			checked++
			failure := errors.New(cause)
			if stderr != "" {
				failure = &foldedCause{text: cause, exit: &exec.ExitError{Stderr: []byte(stderr)}}
			}
			wrapped := WrapCommandErrorWithEnvironment([]byte(stdout), failure, nil)
			if strings.Contains(wrapped.Error(), foldedToken) {
				t.Fatalf("token visible for stdout=%q stderr=%q cause=%q", stdout, stderr, cause)
			}
		}
	}
	return checked
}

func foldedJoin(stdout, stderr, cause string) string {
	joined := ""
	if stdout != "" {
		joined += stdout + "\n"
	}
	if stderr != "" {
		joined += stderr + "\n"
	}
	return joined + cause
}
