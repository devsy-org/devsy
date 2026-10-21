//nolint:gosec // These regressions execute generated scripts and executable fixtures under private test directories.
package inject

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type BootstrapSuite struct{ suite.Suite }

func TestBootstrapSuite(t *testing.T) { suite.Run(t, new(BootstrapSuite)) }

func (s *BootstrapSuite) SetupSuite() {
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		s.T().Skip("POSIX /bin/sh is unavailable")
	}
}

func (s *BootstrapSuite) TestLiteralRoundTrip() {
	for _, value := range []string{"", "a'b\\c\n雪\r", "$(exit 42)`exit 43`\";$HOME()"} {
		s.Run(fmt.Sprintf("%q", value), func() {
			quoted, err := QuotePOSIXLiteral(value)
			s.Require().NoError(err)
			cmd := exec.CommandContext(s.T().Context(), "/bin/sh", "-c", "printf %s "+quoted)
			output, err := cmd.Output()
			s.Require().NoError(err)
			s.Equal(value, string(output))
		})
	}
	_, err := QuotePOSIXLiteral("a\x00b")
	s.Error(err)
}

func bootstrapParams(installPath string) *Params {
	return &Params{
		Command: "cat", AgentRemotePath: installPath, DownloadURLs: &DownloadURLs{},
		ExistsCheck: "true", ShouldChmodPath: true,
	}
}

func runBootstrap(
	t *testing.T,
	params *Params,
	stdin []byte,
	faultHooks ...string,
) (string, string, error) {
	t.Helper()
	script, err := GenerateScript(Script, params)
	require.NoError(t, err)
	if len(faultHooks) > 0 {
		hooks := strings.Join(faultHooks, "\n") + "\n"
		quotedHooks, quoteErr := QuotePOSIXLiteral(hooks)
		require.NoError(t, quoteErr)
		const installerAssignment = "installer_code=$1"
		require.Equal(t, 1, strings.Count(script, installerAssignment))
		// BusyBox standalone applets bypass PATH doubles; functions override them in both shells.
		script = hooks + strings.Replace(
			script, installerAssignment, "installer_code="+quotedHooks+"\"$1\"", 1,
		)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	return stdout.String(), stderr.String(), err
}

func binaryInput(binary []byte) []byte {
	return append(fmt.Appendf(nil, "pong\n%d\n", len(binary)), binary...)
}

func bootstrapFailureHooks(chmodFails bool) string {
	hooks := "curl() { return 1; }\nsleep() { :; }\n"
	if chmodFails {
		hooks += "chmod() { printf '%s\\n' 'injected chmod failure' >&2; return 1; }\n"
	}
	return hooks
}

func (s *BootstrapSuite) TestHostilePathAndLiveBinaryStream() {
	const liveInput = "live-tunnel\x00bytes\n"
	root := s.T().TempDir()
	sentinel := filepath.Join(root, "substitution-ran")
	installPath := filepath.Join(root, "bin ' \\ \n雪 $(touch substitution-ran)", "agent '\"`$()")
	params := bootstrapParams(installPath)
	params.DownloadURLs = &DownloadURLs{Base: "https://invalid/'\"`$(touch " + sentinel + ")\\\r\n"}
	params.Command = fmt.Sprintf("printf '%%s\\n' \"$DEVSY_AGENT_URL\"; head -c %d", len(liveInput))
	binary := make([]byte, 4096)
	_, err := rand.Read(binary)
	s.Require().NoError(err)
	copy(binary, []byte{0, '\'', '\\', '"', '$', '\n'})
	var rawOutput, stderr syncBuffer
	var userOutput bytes.Buffer
	executed, err := Inject(s.T().Context(), InjectOptions{
		Exec: func(ctx context.Context, script string, stdin io.Reader, stdout, diagnostics io.Writer) error {
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, io.MultiWriter(
				stdout,
				&rawOutput,
			), diagnostics
			return cmd.Run()
		},
		LocalFile:    func(bool) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(binary)), nil },
		ScriptParams: params,
		Stdin:        strings.NewReader(liveInput),
		Stdout:       &userOutput,
		Stderr:       &stderr,
		Timeout:      5 * time.Second,
	})
	s.Require().NoError(err, stderr.String())
	s.True(executed)
	stdout := rawOutput.String()
	s.True(strings.HasPrefix(stdout, "ping\nARM-"), stdout)
	s.Equal(1, strings.Count(stdout, "done\n"))
	s.True(
		strings.HasSuffix(stdout, "done\n"+params.DownloadURLs.Base+"\n"+liveInput),
		stdout,
	)
	s.Equal(params.DownloadURLs.Base+"\n"+liveInput, userOutput.String())
	installed, err := os.ReadFile(installPath)
	s.Require().NoError(err)
	s.Equal(binary, installed)
	info, err := os.Stat(installPath)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o755), info.Mode().Perm())
	_, err = os.Stat(sentinel)
	s.True(os.IsNotExist(err))
}

func (s *BootstrapSuite) TestFailedReplacementPreservesAgent() {
	const chmodFailure = "chmod"
	for _, failure := range []string{"short-transfer", chmodFailure} {
		s.Run(failure, func() {
			installPath := filepath.Join(s.T().TempDir(), "agent")
			original := []byte("old-working-agent")
			s.Require().NoError(os.WriteFile(installPath, original, 0o750))
			before, err := os.Stat(installPath)
			s.Require().NoError(err)
			input := binaryInput([]byte("new-agent"))
			if failure == "short-transfer" {
				input = []byte("pong\n100\nshort")
			}
			stdout, stderr, err := runBootstrap(
				s.T(),
				bootstrapParams(installPath),
				input,
				bootstrapFailureHooks(failure == chmodFailure),
			)
			s.Error(err, stderr)
			if failure == chmodFailure {
				s.Contains(stderr, "injected chmod failure")
				s.Contains(stderr, "Failed to make temporary agent executable")
			} else {
				s.Contains(stderr, "Failed to receive complete agent binary")
			}
			s.NotContains(stdout, "done\n")
			installed, err := os.ReadFile(installPath)
			s.Require().NoError(err)
			s.Equal(original, installed)
			after, err := os.Stat(installPath)
			s.Require().NoError(err)
			s.Equal(before.Mode(), after.Mode())
			files, err := filepath.Glob(installPath + ".*")
			s.Require().NoError(err)
			s.Empty(files)
		})
	}
}

func (s *BootstrapSuite) TestDownloadFailureFallsBackToStdin() {
	installPath := filepath.Join(s.T().TempDir(), "agent")
	params := bootstrapParams(installPath)
	params.PreferAgentDownload = true
	stdout, stderr, err := runBootstrap(
		s.T(),
		params,
		binaryInput([]byte("replacement")),
		bootstrapFailureHooks(false),
	)
	s.Require().NoError(err, stderr)
	s.Contains(stderr, "Download failed, attempting stdin injection")
	s.Equal(3, strings.Count(stderr, "Download attempt "))
	s.Contains(stdout, "done\n")
	installed, err := os.ReadFile(installPath)
	s.Require().NoError(err)
	s.Equal("replacement", string(installed))
}

func (s *BootstrapSuite) TestWarmAgentDoesNotInstall() {
	params := bootstrapParams(filepath.Join(s.T().TempDir(), "missing", "agent"))
	params.ExistsCheck = "false"
	stdout, stderr, err := runBootstrap(s.T(), params, []byte("pong\nlive\n"))
	s.Require().NoError(err, stderr)
	s.Equal("ping\ndone\nlive\n", stdout)
}

func (s *BootstrapSuite) TestGenerateRejectsNULAndParses() {
	params := bootstrapParams("/tmp/agent")
	fields := []*string{
		&params.AgentRemotePath,
		&params.DownloadURLs.Base,
		&params.DownloadURLs.Amd,
		&params.DownloadURLs.Arm,
		&params.Command,
		&params.ExistsCheck,
	}
	for _, field := range fields {
		old := *field
		*field = "bad\x00value"
		_, err := GenerateScript(Script, params)
		s.Error(err)
		*field = old
	}
	script, err := GenerateScript(Script, params)
	s.Require().NoError(err)
	cmd := exec.CommandContext(s.T().Context(), "/bin/sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	s.NoError(err, string(output))
}

func (s *BootstrapSuite) TestInvalidLiteralDoesNotReportExecution() {
	params := bootstrapParams("bad\x00path")
	executed, err := Inject(s.T().Context(), InjectOptions{
		Exec: func(context.Context, string, io.Reader, io.Writer, io.Writer) error {
			s.T().Fatal("invalid literal must fail before execution")
			return nil
		},
		ScriptParams: params,
	})
	s.Error(err)
	s.False(executed)
}
