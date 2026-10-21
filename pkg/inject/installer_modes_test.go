//nolint:gosec // Generated scripts and privilege-command doubles execute only in private test directories.
package inject

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testSudoMode = "sudo"
	testSuMode   = "su"
)

const installerModeDoubles = `
sudo() {
    [ "$#" -ge 6 ] && [ "$1" = -n ] && [ "$2" = -E ] &&
        [ "$3" = /bin/sh ] && [ "$4" = -c ] && [ "$6" = devsy-install ] || return 98
    printf 'sudo\n' >> "$INSTALLER_TEST_MARKER" || return 99
    shift 2
    "$@"
}
su() {
    [ "$#" -eq 5 ] && [ "$1" = -s ] && [ "$2" = /bin/sh ] &&
        [ "$3" = -c ] && [ "$5" = root ] || return 98
    printf 'su\n' >> "$INSTALLER_TEST_MARKER" || return 99
    "$2" -c "$4"
}
`

func installerModeFixture(t *testing.T, script, mode, marker string) string {
	t.Helper()
	require.Contains(t, []string{testSudoMode, testSuMode}, mode)
	const assignment = "INSTALLER_MODE=direct"
	require.Equal(t, 1, strings.Count(script, assignment))
	quotedMarker, err := QuotePOSIXLiteral(marker)
	require.NoError(t, err)
	return "INSTALLER_TEST_MARKER=" + quotedMarker + "\n" + installerModeDoubles +
		strings.Replace(script, assignment, "INSTALLER_MODE="+mode, 1)
}

func (s *BootstrapSuite) TestPrivilegedInstallerModesLiveStream() {
	for _, mode := range []string{testSudoMode, testSuMode} {
		s.Run(mode, func() { s.checkInstallerModeLiveStream(mode) })
	}
}

func (s *BootstrapSuite) checkInstallerModeLiveStream(mode string) {
	const liveInput = "live-tunnel\x00bytes\n"
	root := s.T().TempDir()
	marker := filepath.Join(root, "mode-used")
	params := bootstrapParams(filepath.Join(
		root, "dir '\\\n雪", "agent $(touch substitution-ran)`touch substitution-ran`",
	))
	params.DownloadURLs.Base = "https://invalid/'\"`$(touch substitution-ran)\\\r\n雪"
	params.Command = fmt.Sprintf("printf '%%s\\n' \"$DEVSY_AGENT_URL\"; head -c %d", len(liveInput))
	rendered, err := GenerateScript(Script, params)
	s.Require().NoError(err)
	fixture := installerModeFixture(s.T(), rendered, mode, marker)
	payload := bytes.Repeat([]byte{0, '\'', '\\', '"', '$', '\n', '\r', 255}, 512)
	var rawOutput, stderr syncBuffer
	var userOutput bytes.Buffer
	executed, err := Inject(s.T().Context(), InjectOptions{
		Exec: func(ctx context.Context, script string, stdin io.Reader, stdout, diagnostics io.Writer) error {
			if script != rendered {
				return fmt.Errorf("unexpected rendered bootstrap")
			}
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", fixture)
			cmd.Dir = root
			cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, io.MultiWriter(
				stdout,
				&rawOutput,
			), diagnostics
			return cmd.Run()
		},
		LocalFile:    func(bool) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(payload)), nil },
		ScriptParams: params,
		Stdin:        strings.NewReader(liveInput),
		Stdout:       &userOutput,
		Stderr:       &stderr,
		Timeout:      5 * time.Second,
	})
	s.Require().NoError(err, stderr.String())
	s.True(executed)
	s.Empty(stderr.String())
	s.Equal(params.DownloadURLs.Base+"\n"+liveInput, userOutput.String())
	s.True(strings.HasPrefix(rawOutput.String(), "ping\nARM-"), rawOutput.String())
	s.Equal(1, strings.Count(rawOutput.String(), "done\n"))
	s.True(strings.HasSuffix(rawOutput.String(), "done\n"+userOutput.String()))
	installed, err := os.ReadFile(params.AgentRemotePath)
	s.Require().NoError(err)
	s.Equal(payload, installed)
	assertInstallerModeUsed(s.T(), marker, mode)
	_, err = os.Stat(filepath.Join(root, "substitution-ran"))
	s.True(os.IsNotExist(err))
}

func (s *BootstrapSuite) TestPrivilegedInstallerFailureStatus() {
	for _, mode := range []string{testSudoMode, testSuMode} {
		s.Run(mode, func() {
			root := s.T().TempDir()
			marker := filepath.Join(root, "mode-used")
			script, err := GenerateScript(Script, bootstrapParams(filepath.Join(root, "agent")))
			s.Require().NoError(err)
			script = installerModeFixture(s.T(), script, mode, marker)
			const entry = "\nmain\n"
			s.Require().Equal(1, strings.Count(script, entry))
			script = strings.Replace(script, entry, "\nrun_installer_script 'exit \"$1\"' 37\n", 1)
			ctx, cancel := context.WithTimeout(s.T().Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			s.Require().ErrorAs(err, &exitErr)
			s.Equal(37, exitErr.ExitCode())
			s.Empty(output)
			assertInstallerModeUsed(s.T(), marker, mode)
		})
	}
}

func assertInstallerModeUsed(t *testing.T, marker, mode string) {
	t.Helper()
	used, err := os.ReadFile(marker)
	require.NoError(t, err)
	lines := strings.Fields(string(used))
	require.NotEmpty(t, lines)
	for _, line := range lines {
		require.Equal(t, mode, line)
	}
}
