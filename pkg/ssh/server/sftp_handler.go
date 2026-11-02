package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/ssh"
	"github.com/pkg/sftp"
)

var newSFTPCommand = func(_ context.Context, home string) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &exec.Cmd{
		Path: executable,
		Args: []string{executable, "internal", "agent", "container", "sftp-server", "--home", home},
	}, nil
}

type sftpExecIdentity struct {
	home         string
	credential   string
	account      *user.User
	hasGroupSpec bool
}

func sftpHandler(sess ssh.Session) {
	err := runSFTP(sess)
	if err != nil {
		log.Debugf("sftp session failed: %T (exit %d)", err, exitCode(err))
	}
	if err := sess.Exit(exitCode(err)); err != nil {
		log.Debugf("sftp session exit failed: %T", err)
	}
}

func runSFTP(sess ssh.Session) error {
	identity, err := resolveSFTPIdentity(sess.User())
	if err != nil {
		return err
	}
	cmd, err := newSFTPCommand(sess.Context(), identity.home)
	if err != nil {
		return err
	}
	if cmd == nil {
		return errors.New("missing SFTP command")
	}
	if err := prepareSFTPCommand(cmd, identity); err != nil {
		return err
	}
	cmd.Stdout = sess
	cmd.Stderr = sess.Stderr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	stopCancellation := context.AfterFunc(sess.Context(), func() { _ = cmd.Process.Kill() })
	defer stopCancellation()
	go func() {
		_, _ = io.Copy(stdin, sess)
		_ = stdin.Close()
	}()
	return cmd.Wait()
}

func resolveSFTPIdentity(spec string) (sftpExecIdentity, error) {
	if spec == "" {
		return sftpExecIdentity{}, errors.New("missing SFTP user")
	}
	requested, handled, err := parseRequestedExecIdentity(spec)
	if err != nil {
		return sftpExecIdentity{}, err
	}
	if !handled {
		requested.user = spec
	}
	account, err := lookupSFTPAccount(requested)
	if err != nil {
		return sftpExecIdentity{}, err
	}
	identity := sftpExecIdentity{
		home:         "/",
		credential:   spec,
		account:      account,
		hasGroupSpec: requested.hasGroupSpec,
	}
	if account != nil {
		if account.HomeDir != "" {
			identity.home = account.HomeDir
		}
		if !requested.hasGroupSpec {
			identity.credential = account.Uid
		}
	}
	return identity, nil
}

func lookupSFTPAccount(requested requestedExecIdentity) (*user.User, error) {
	if !requested.userIsID {
		return user.Lookup(requested.user)
	}
	uid, err := parseExecID(requested.user)
	if err != nil {
		return nil, err
	}
	account, _ := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	return account, nil
}

func prepareSFTPCommand(cmd *exec.Cmd, identity sftpExecIdentity) error {
	if runtime.GOOS == "windows" && identity.account != nil && !identity.hasGroupSpec {
		return prepareCurrentSFTPUser(cmd, identity)
	}
	handled, err := prepareExecIdentity(cmd, identity.credential)
	if err != nil {
		return err
	}
	if !handled {
		return fmt.Errorf("SFTP identity %q cannot be applied", identity.credential)
	}
	if runtime.GOOS != "windows" {
		cmd.Dir = "/"
	}
	return nil
}

func prepareCurrentSFTPUser(cmd *exec.Cmd, identity sftpExecIdentity) error {
	current, err := user.Current()
	if err != nil {
		return err
	}
	if identity.account.Uid != current.Uid || identity.account.Username != current.Username {
		return errors.New("SFTP user switching is not supported")
	}
	cmd.Env = mergeSessionEnvironment(cmd.Environ(), []string{
		"HOME=" + identity.home,
		"USER=" + identity.account.Username,
		"LOGNAME=" + identity.account.Username,
	}, nil)
	return nil
}

func ServeSFTP(input io.ReadWriteCloser, home string) error {
	server, err := sftp.NewServer(input, sftp.WithServerWorkingDirectory(home))
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()

	err = server.Serve()
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
