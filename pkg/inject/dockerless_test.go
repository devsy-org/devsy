//nolint:gosec // Generated scripts and interpreter fixtures execute only under private test directories.
package inject

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	standardInterpreter = "standard"
	bundledInterpreter  = "bundled"
)

type installerShellCase struct {
	name, flag, selected string
	standard, bundled    bool
}

func (s *BootstrapSuite) TestInstallerShellSelection() {
	for _, tc := range []installerShellCase{
		{name: "ordinary", standard: true, bundled: true, selected: standardInterpreter},
		{
			name: "dockerless keeps standard", flag: strconv.FormatBool(true),
			standard: true, bundled: true, selected: standardInterpreter,
		},
		{name: "dockerless bundled", flag: strconv.FormatBool(true), bundled: true, selected: bundledInterpreter},
		{name: "ordinary missing standard", bundled: true},
		{name: "disabled dockerless", flag: "false", bundled: true},
		{name: "missing both", flag: strconv.FormatBool(true)},
	} {
		s.Run(tc.name, func() {
			root := s.T().TempDir()
			installPath := filepath.Join(root, "agent '\\\n雪 $(touch substitution-ran)")
			original := []byte("working-agent")
			s.Require().NoError(os.WriteFile(installPath, original, 0o750))
			params := bootstrapParams(installPath)
			params.Command = "printf 'ready\\n'"
			script, err := GenerateScript(Script, params)
			s.Require().NoError(err)
			script = installerShellFixture(s.T(), script, root, tc)
			ctx, cancel := context.WithTimeout(s.T().Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "DOCKERLESS="+tc.flag)
			payload := bytes.Repeat([]byte{0, '\'', '\\', '$', '\n', 255}, 700)
			cmd.Stdin = bytes.NewReader(binaryInput(payload))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err = cmd.Run()
			if tc.selected == "" {
				s.Require().Error(err)
				s.Contains(stderr.String(), "requires executable /bin/sh")
				s.NotContains(stderr.String(), "su:")
				s.Equal("ping\n", stdout.String())
				payload = original
			} else {
				s.Require().NoError(err, stderr.String())
				s.True(strings.HasSuffix(stdout.String(), "done\nready\n"), stdout.String())
				s.Equal(1, strings.Count(stdout.String(), "done\n"))
			}
			installed, readErr := os.ReadFile(installPath)
			s.Require().NoError(readErr)
			s.Equal(payload, installed)
			for _, name := range []string{standardInterpreter, bundledInterpreter} {
				_, statErr := os.Stat(filepath.Join(root, name+"-used"))
				s.Equal(name == tc.selected, statErr == nil)
			}
			_, statErr := os.Stat(filepath.Join(root, "substitution-ran"))
			s.True(os.IsNotExist(statErr))
		})
	}
}

func installerShellFixture(t *testing.T, script, root string, tc installerShellCase) string {
	t.Helper()
	for _, interpreter := range []struct {
		assignment, name string
		available        bool
	}{
		{"INSTALLER_SHELL=/bin/sh", standardInterpreter, tc.standard},
		{"DOCKERLESS_SHELL=/.dockerless/bin/sh", bundledInterpreter, tc.bundled},
	} {
		fixture := filepath.Join(root, interpreter.name+" shell '")
		if interpreter.available {
			marker, quoteErr := QuotePOSIXLiteral(filepath.Join(root, interpreter.name+"-used"))
			require.NoError(t, quoteErr)
			require.NoError(t, os.WriteFile(fixture, []byte(
				"#!/bin/sh\nprintf x >> "+marker+"\nexec /bin/sh \"$@\"\n",
			), 0o700))
		}
		quoted, quoteErr := QuotePOSIXLiteral(fixture)
		require.NoError(t, quoteErr)
		require.Equal(t, 1, strings.Count(script, interpreter.assignment))
		// Model an isolated target's fixed paths without changing the host filesystem.
		script = strings.Replace(script, interpreter.assignment, strings.SplitN(
			interpreter.assignment, "=", 2,
		)[0]+"="+quoted, 1)
	}
	return script
}
