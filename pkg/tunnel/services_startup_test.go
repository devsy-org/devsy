package tunnel

import (
	"errors"
	"strings"
	"testing"

	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/stretchr/testify/require"
)

func TestServicesStartupClassifiesOnlyUnsupportedGitHubFlags(t *testing.T) {
	const unknownOwner = `{"kind":"error","message":"unknown flag: --host-owner"}`
	exit := &devssh.ExitError{ExitCode: 1}
	for _, tc := range []struct {
		name, output string
		err          error
		unsupported  bool
	}{
		{"legacy-owner", unknownOwner, exit, true},
		{"legacy-helper", `{"kind":"error","message":"unknown flag: --configure-github-helper"}`, exit, true},
		{"preceding-log", "{\"kind\":\"info\",\"message\":\"starting\"}\n" + unknownOwner, exit, true},
		{"unrelated-flag", `{"kind":"error","message":"unknown flag: --configure-git-helper"}`, exit, false},
		{"runtime-error", `{"kind":"error","message":"connection reset"}`, exit, false},
		{"not-error-record", `{"kind":"info","message":"unknown flag: --host-owner"}`, exit, false},
		{"partial-output", `{"kind":"error","message":"unknown flag: --host-owner"`, exit, false},
		{"unstructured", "unknown flag: --host-owner", exit, false},
		{"transport-error", unknownOwner, errors.New("SSH disconnected"), false},
		{"successful-command", unknownOwner, nil, false},
		{"overflow", unknownOwner + strings.Repeat(" ", 4096), exit, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := &boundedPreparationOutput{}
			_, err := output.Write([]byte(tc.output))
			require.NoError(t, err)
			got := classifyServicesStartupError(tc.err, output)
			require.Equal(t, tc.unsupported, errors.Is(got, errUnsupportedGitHubServiceOptions))
			require.ErrorIs(t, got, tc.err)
		})
	}
}
