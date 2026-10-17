//go:build linux

package server

import (
	"io"
	"os"
	"os/user"
	"strings"
	"testing"

	"github.com/devsy-org/ssh"
	"github.com/stretchr/testify/require"
)

type identityExecutionSession struct {
	commandLogSession
	identity string
}

func (s *identityExecutionSession) User() string    { return s.identity }
func (*identityExecutionSession) Environ() []string { return nil }
func (*identityExecutionSession) RawCommand() string {
	return `printf '%s:%s:%s' "$(id -u)" "$(id -g)" "$(id -G)"`
}

func TestSSHExecAppliesNumericCredentialsToChild(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing guest credentials requires root")
	}
	account, err := user.Lookup("nobody")
	require.NoError(t, err)
	unknownUID := "3912345678"
	_, err = user.LookupId(unknownUID)
	require.Error(t, err, "fixture UID must not have a passwd entry")
	for _, identity := range []string{account.Uid, account.Uid + ":0", unknownUID + ":0"} {
		for _, usePTY := range []bool{false, true} {
			name := identity + "/non-PTY"
			if usePTY {
				name = identity + "/PTY"
			}
			t.Run(name, func(t *testing.T) {
				session := &identityExecutionSession{identity: identity}
				if usePTY {
					input, writer := io.Pipe()
					session.input = input
					t.Cleanup(func() { _ = writer.Close(); _ = input.Close() })
				}
				server := &server{currentUser: "root", shell: []string{execTestShell}, workdir: "/"}
				cmd, err := server.getCommandWithSecretEnvironment(session, usePTY, nil)
				require.NoError(t, err)
				if usePTY {
					err = execPTY(ptyExecParams{
						sess: session, cmd: cmd,
						ptyReq: ssh.Pty{Term: "xterm", Window: ssh.Window{Height: 24, Width: 80}},
					})
				} else {
					err = execNonPTY(session, cmd)
				}
				require.NoError(t, err)
				parts := strings.Split(strings.TrimSpace(session.output.String()), ":")
				require.Len(t, parts, 3)
				if identity == account.Uid {
					require.Equal(t, account.Uid, parts[0])
					require.Equal(t, account.Gid, parts[1])
					groups, err := account.GroupIds()
					require.NoError(t, err)
					require.ElementsMatch(t, groups, strings.Fields(parts[2]))
				} else {
					uid, _, _ := strings.Cut(identity, ":")
					require.Equal(t, uid, parts[0])
					require.Equal(t, "0", parts[1])
					require.Equal(
						t,
						"0",
						parts[2],
						"explicit group must not inherit root supplementary groups",
					)
				}
			})
		}
	}
}
