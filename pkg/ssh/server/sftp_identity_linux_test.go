//go:build linux

package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/ssh"
	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

const sftpTestChildEnvironment = "DEVSY_SFTP_TEST_CHILD"

type sftpTestProcessIO struct {
	io.Reader
	io.Writer
}

func (sftpTestProcessIO) Close() error { return nil }

func TestSFTPChild(t *testing.T) {
	if os.Getenv(sftpTestChildEnvironment) != "1" {
		return
	}
	if len(os.Args) < 2 {
		os.Exit(1)
	}
	if err := ServeSFTP(
		sftpTestProcessIO{Reader: os.Stdin, Writer: os.Stdout},
		os.Args[len(os.Args)-1],
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func sftpRootAccessibleTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "devsy-sftp-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	require.NoError(t, os.Chmod(dir, 0o755))
	return dir
}

func configureSFTPTestChild(t *testing.T, accessibleToOtherUsers bool) {
	t.Helper()
	t.Setenv(sftpTestChildEnvironment, "1")
	executable, err := os.Executable()
	require.NoError(t, err)
	if accessibleToOtherUsers {
		dir := sftpRootAccessibleTempDir(t)
		original, err := os.Open(executable)
		require.NoError(t, err)
		defer original.Close()
		executable = filepath.Join(dir, "sftp-test-child")
		copy, err := os.OpenFile(executable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		require.NoError(t, err)
		_, err = io.Copy(copy, original)
		require.NoError(t, err)
		require.NoError(t, copy.Close())
		require.NoError(t, os.Chmod(executable, 0o755))
	}
	previous := newSFTPCommand
	newSFTPCommand = func(ctx context.Context, home string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, executable, "-test.run=^TestSFTPChild$", "--", home), nil
	}
	t.Cleanup(func() { newSFTPCommand = previous })
}

func openSFTPTestSession(
	t *testing.T,
	username string,
) (*gossh.Session, io.Reader, io.WriteCloser) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	sshd := buildSSHServer("")
	sshd.HostSigners = []ssh.Signer{signer}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() { finished <- sshd.Serve(listener) }()
	t.Cleanup(func() {
		require.NoError(t, sshd.Close())
		err := <-finished
		require.True(
			t,
			errors.Is(err, ssh.ErrServerClosed) || errors.Is(err, net.ErrClosed),
			"server shutdown: %v",
			err,
		)
	})
	client, err := gossh.Dial("tcp", listener.Addr().String(), &gossh.ClientConfig{
		User:            username,
		HostKeyCallback: gossh.FixedHostKey(signer.PublicKey()),
		Timeout:         5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	session, err := client.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	stdin, err := session.StdinPipe()
	require.NoError(t, err)
	stdout, err := session.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, session.RequestSubsystem("sftp"))
	return session, stdout, stdin
}

func newSFTPTestClient(t *testing.T, stdout io.Reader, stdin io.WriteCloser) *sftp.Client {
	t.Helper()
	client, err := sftp.NewClientPipe(stdout, stdin)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func assertSFTPFileIdentity(t *testing.T, client *sftp.Client, dir string, account *user.User) {
	t.Helper()
	path := filepath.Join(dir, "created-via-sftp")
	file, err := client.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	require.NoError(t, err)
	_, err = file.Write([]byte("sftp identity"))
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.NoError(t, client.Chmod(path, 0o600))
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "sftp identity", string(contents))
	info, err := os.Stat(path)
	require.NoError(t, err)
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	require.NoError(t, err)
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	require.NoError(t, err)
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	require.Equal(t, uint32(uid), stat.Uid)
	require.Equal(t, uint32(gid), stat.Gid)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSFTPRequestedUserOwnsFilesAndCannotReadRootPrivatePath(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("switching to a separate account requires root")
	}
	nobody, err := user.Lookup("nobody")
	if err != nil {
		t.Skipf("nobody account unavailable: %v", err)
	}
	if nobody.Uid == "0" {
		t.Skip("nobody account has root UID")
	}
	configureSFTPTestChild(t, true)
	dir := sftpRootAccessibleTempDir(t)
	writable := filepath.Join(dir, "writable")
	require.NoError(t, os.Mkdir(writable, 0o777))
	require.NoError(t, os.Chmod(writable, 0o777))
	private := filepath.Join(dir, "root-private")
	require.NoError(t, os.Mkdir(private, 0o700))
	secret := filepath.Join(private, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("root only"), 0o600))

	_, stdout, stdin := openSFTPTestSession(t, nobody.Username)
	client := newSFTPTestClient(t, stdout, stdin)
	assertSFTPFileIdentity(t, client, writable, nobody)
	_, err = client.Open(secret)
	require.ErrorIs(t, err, os.ErrPermission)
	require.NoError(t, client.Close())
}

func TestSFTPCurrentUserAndInvalidIdentity(t *testing.T) {
	current, err := user.Current()
	require.NoError(t, err)
	configureSFTPTestChild(t, false)

	t.Run("current user", func(t *testing.T) {
		_, stdout, stdin := openSFTPTestSession(t, current.Username)
		client := newSFTPTestClient(t, stdout, stdin)
		assertSFTPFileIdentity(t, client, t.TempDir(), current)
		if info, err := os.Stat(current.HomeDir); err == nil && info.IsDir() {
			workingDir, err := client.Getwd()
			require.NoError(t, err)
			require.Equal(t, current.HomeDir, workingDir)
		}
		require.NoError(t, client.Close())
	})

	t.Run("unknown account", func(t *testing.T) {
		invalid := fmt.Sprintf("devsy-sftp-missing-%d", os.Getpid())
		_, err := user.Lookup(invalid)
		require.Error(t, err)
		_, stdout, stdin := openSFTPTestSession(t, invalid)
		_, err = sftp.NewClientPipe(stdout, stdin)
		require.Error(t, err)
	})

	if os.Geteuid() != 0 {
		t.Run("non-switchable account", func(t *testing.T) {
			_, stdout, stdin := openSFTPTestSession(t, "root")
			_, err := sftp.NewClientPipe(stdout, stdin)
			require.Error(t, err)
		})
	}
}
