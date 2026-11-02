package ghcredentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"al.essio.dev/pkg/shellescape"
)

const (
	wrapperPath   = "/usr/local/bin/gh"
	backupSuffix  = ".devsy-original"
	wrapperHeader = "#!/bin/sh\n# devsy gh relay\n"
)

type InstallResult struct {
	Status string `json:"status"`
}

func Install(ctx context.Context, port int, targetUser string) (InstallResult, error) {
	if runtime.GOOS != "linux" {
		return InstallResult{Status: stateUnavailable}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	binary, err := os.Executable()
	if err != nil {
		return InstallResult{Status: stateUnavailable}, boundary("install.binary", err)
	}
	backend, err := discoverForUser(ctx, binary, targetUser)
	if err != nil {
		return InstallResult{Status: stateUnavailable}, boundary("install.discovery", err)
	}
	if backend.Status != stateAvailable {
		return backend, nil
	}
	owned, _ := isWrapper(wrapperPath)
	if os.Geteuid() != 0 {
		err = installWithSudo(ctx, binary, port)
	} else {
		err = installPrepared(ctx, binary, port)
	}
	if err != nil {
		return InstallResult{Status: stateUnavailable}, err
	}
	if owned {
		return InstallResult{Status: "already_installed"}, nil
	}
	return InstallResult{Status: "installed"}, nil
}

func DiscoverBackend() InstallResult {
	if _, err := discoverBackend(wrapperPath); err != nil {
		return InstallResult{Status: "backend_missing"}
	}
	return InstallResult{Status: stateAvailable}
}

func discoverBackend(wrapper string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		path := filepath.Join(dir, "gh")
		if _, err := executableInfo(path); err != nil {
			continue
		}
		own, err := isWrapper(path)
		if err != nil {
			continue
		}
		if !own {
			return filepath.Abs(path)
		}
		if backup := wrapperBackup(path, nil); backup != "" {
			return filepath.Abs(backup)
		}
	}
	if backup := wrapperBackup(wrapper, nil); backup != "" {
		return filepath.Abs(backup)
	}
	return "", exec.ErrNotFound
}

func discoverForUser(ctx context.Context, binary, targetUser string) (InstallResult, error) {
	u, err := user.Lookup(targetUser)
	if err != nil {
		return InstallResult{}, err
	}
	if u.Uid == strconv.Itoa(os.Geteuid()) {
		return DiscoverBackend(), nil
	}
	if os.Geteuid() != 0 {
		return InstallResult{}, errors.New("GitHub CLI target user differs from service user")
	}
	return discoverWithLoginShell(ctx, binary, targetUser)
}

func discoverWithLoginShell(ctx context.Context, binary, targetUser string) (InstallResult, error) {
	// Separate the response even when the login profile prints without a newline.
	//nolint:gosec // su drops to the requested guest user before reading their login environment.
	cmd := exec.CommandContext(
		ctx,
		"su",
		"-",
		targetUser,
		"-c",
		"printf '\\n'; exec "+shellescape.Quote(binary)+" internal gh-backend",
	)
	cmd.WaitDelay = 250 * time.Millisecond
	out := &tokenOutput{}
	if err := runTokenProbe(cmd, out); err != nil {
		if ctx.Err() != nil {
			return InstallResult{}, ctx.Err()
		}
		return InstallResult{}, err
	}
	response := bytes.TrimSpace(out.data)
	response = response[bytes.LastIndexByte(response, '\n')+1:]
	var result InstallResult
	if out.overflow || json.Unmarshal(response, &result) != nil {
		return InstallResult{}, errInvalidConfig
	}
	return result, nil
}

func InstallPrepared(ctx context.Context, port int) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	return installPrepared(ctx, binary, port)
}

func installPrepared(ctx context.Context, binary string, port int) error {
	return boundary("install.wrapper", installWrapperContext(ctx, wrapperPath, binary, port))
}

func installWithSudo(ctx context.Context, binary string, port int) error {
	//nolint:gosec // Execute only the installed agent helper; never execute a user-selected gh as root.
	cmd := exec.CommandContext(
		ctx,
		"sudo",
		"-n",
		binary,
		"internal",
		"install-gh-wrapper",
		strconv.Itoa(port),
	)
	cmd.WaitDelay = 250 * time.Millisecond
	err := runTokenProbe(cmd, io.Discard)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return boundary("install.sudo", err)
}

func installWrapper(path, binary string, port int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return installWrapperContext(ctx, path, binary, port)
}

func installWrapperContext(ctx context.Context, path, binary string, port int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	// #nosec G301 -- Public executable directory without authentication data.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	l, err := lock(ctx, filepath.Join(dir, ".devsy-gh-install.lock"))
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()

	staged, err := stageWrapper(dir, binary, port)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(staged) }()
	moved, err := backupOriginal(path)
	if err != nil {
		return err
	}
	if err := os.Rename(staged, path); err != nil {
		if moved {
			return errors.Join(err, os.Rename(path+backupSuffix, path))
		}
		return err
	}
	return nil
}

func stageWrapper(dir, binary string, port int) (name string, err error) {
	script := fmt.Sprintf(
		wrapperHeader+"export DEVSY_CREDENTIALS_SERVER_PORT=%d\nexec %s internal gh \"$0\" \"$@\"\n",
		port,
		shellescape.Quote(binary),
	)
	f, err := os.CreateTemp(dir, ".devsy-gh-wrapper-*")
	if err != nil {
		return "", err
	}
	name = f.Name()
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if _, err = f.WriteString(script); err != nil {
		return name, err
	}
	if err = f.Chmod(0o755); err != nil {
		return name, err
	}
	err = f.Close()
	return name, err
}

func backupOriginal(path string) (bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	own, err := isWrapper(path)
	if err != nil || own {
		return false, err
	}
	if _, err := executableInfo(path); err != nil {
		return false, err
	}
	if err := os.Rename(path, path+backupSuffix); err != nil {
		return false, err
	}
	return true, nil
}

func isWrapper(path string) (bool, error) {
	f, err := resolvedFile(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	header := make([]byte, len(wrapperHeader))
	_, err = io.ReadFull(f, header)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return string(header) == wrapperHeader, nil
}
