package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	microsandboxVersion = "v0.7.7"
	microsandboxURL     = "https://github.com/superradcompany/microsandbox/releases/download/" +
		"v0.7.7/microsandbox-linux-x86_64.tar.gz"
	microsandboxSHA256  = "b3cc4a5e3f52dfdd938a6f67ac4a9a959ddfe304bab56de4964044b8613f01bb"
	microsandboxArchive = "microsandbox-linux-x86_64-v0.7.7.tar.gz"
	microsandboxLibrary = "libkrunfw.so.5.6.1"
)

type installerBundle struct {
	filename string
	url      string
	sha256   string
}

type linuxInstaller struct {
	ctx     context.Context
	command commandRunner
	output  commandOutput
	bundle  installerBundle
}

func installLinux(ctx context.Context, run commandRunner, output commandOutput) error {
	installer := linuxInstaller{
		ctx: ctx, command: run, output: output,
		bundle: installerBundle{
			filename: microsandboxArchive,
			url:      microsandboxURL,
			sha256:   microsandboxSHA256,
		},
	}
	return installer.install()
}

type installerPaths struct {
	runnerTemp string
	archive    string
	githubPath string
	home       string
	binDir     string
	libDir     string
	localBin   string
}

func (installer *linuxInstaller) install() (err error) {
	paths, err := installer.validateEnvironment()
	if err != nil {
		return err
	}
	if err := installer.prepareArchive(paths); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(paths.runnerTemp, "microsandbox-install-")
	if err != nil {
		return fmt.Errorf("create MicroSandbox staging directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	if err := installer.extractArchive(paths.archive, staging); err != nil {
		return err
	}
	if err := installer.installFiles(staging, paths); err != nil {
		return err
	}
	return installer.activate(paths)
}

func (installer *linuxInstaller) validateEnvironment() (installerPaths, error) {
	bundle := installer.bundle
	runnerTemp, err := requiredInstallerEnvironment("RUNNER_TEMP")
	if err != nil {
		return installerPaths{}, err
	}
	if err := existingInstallerDirectory(runnerTemp); err != nil {
		return installerPaths{}, err
	}
	githubPath, err := requiredInstallerEnvironment("GITHUB_PATH")
	if err != nil {
		return installerPaths{}, err
	}
	home, err := requiredInstallerEnvironment("HOME")
	if err != nil {
		return installerPaths{}, err
	}
	if err := validateInstallerBundle(bundle); err != nil {
		return installerPaths{}, err
	}
	installRoot := filepath.Join(home, ".microsandbox")
	return installerPaths{
		runnerTemp: runnerTemp,
		archive:    filepath.Join(runnerTemp, bundle.filename),
		githubPath: githubPath,
		home:       home,
		binDir:     filepath.Join(installRoot, "bin"),
		libDir:     filepath.Join(installRoot, "lib"),
		localBin:   filepath.Join(home, ".local", "bin"),
	}, nil
}

func requiredInstallerEnvironment(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s must be set by the workflow", name)
	}
	return value, nil
}

func existingInstallerDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf(
			"RUNNER_TEMP must name an existing directory: %s",
			path,
		)
	}
	return nil
}

func validateInstallerBundle(bundle installerBundle) error {
	if bundle.filename == "" || filepath.Base(bundle.filename) != bundle.filename ||
		bundle.url == "" {
		return errors.New("invalid pinned MicroSandbox bundle metadata")
	}
	if len(bundle.sha256) != sha256.Size*2 {
		return errors.New("invalid pinned MicroSandbox SHA-256")
	}
	if _, err := hex.DecodeString(bundle.sha256); err != nil {
		return errors.New("invalid pinned MicroSandbox SHA-256")
	}
	return nil
}

func (installer *linuxInstaller) prepareArchive(paths installerPaths) error {
	if _, err := os.Stat(paths.archive); errors.Is(err, os.ErrNotExist) {
		if err := installer.run(
			"curl",
			"-fsSL",
			"--retry",
			"3",
			"-o",
			paths.archive,
			installer.bundle.url,
		); err != nil {
			return fmt.Errorf("download pinned MicroSandbox %s: %w", microsandboxVersion, err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect cached MicroSandbox archive: %w", err)
	}
	actual, err := installerFileSHA256(paths.archive)
	if err != nil {
		return fmt.Errorf("checksum pinned MicroSandbox archive: %w", err)
	}
	if !strings.EqualFold(actual, installer.bundle.sha256) {
		return fmt.Errorf("MicroSandbox archive checksum mismatch: got %s", actual)
	}
	return nil
}

func (installer *linuxInstaller) extractArchive(archive, staging string) error {
	if err := installer.run("tar", "-xzf", archive, "-C", staging); err != nil {
		return fmt.Errorf("extract pinned MicroSandbox archive: %w", err)
	}
	return nil
}

func (installer *linuxInstaller) installFiles(staging string, paths installerPaths) error {
	for _, dir := range []string{paths.binDir, paths.libDir, paths.localBin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create MicroSandbox install directory: %w", err)
		}
	}
	msbPath := filepath.Join(paths.binDir, "msb")
	if err := copyInstallerFile(filepath.Join(staging, "msb"), msbPath, 0o755); err != nil {
		return fmt.Errorf("install MicroSandbox executable: %w", err)
	}
	libraryPath := filepath.Join(paths.libDir, microsandboxLibrary)
	if err := copyInstallerFile(
		filepath.Join(staging, microsandboxLibrary),
		libraryPath,
		0o644,
	); err != nil {
		return fmt.Errorf("install MicroSandbox library: %w", err)
	}
	for _, link := range []struct{ path, target, label string }{
		{filepath.Join(paths.libDir, "libkrunfw.so.5"), microsandboxLibrary, "library ABI"},
		{filepath.Join(paths.libDir, "libkrunfw.so"), "libkrunfw.so.5", "library"},
		{filepath.Join(paths.localBin, "msb"), msbPath, "executable"},
	} {
		if err := replaceInstallerSymlink(link.path, link.target); err != nil {
			return fmt.Errorf("link MicroSandbox %s: %w", link.label, err)
		}
	}
	if err := appendInstallerPath(paths.githubPath, paths.localBin); err != nil {
		return fmt.Errorf("append MicroSandbox to workflow PATH: %w", err)
	}
	return nil
}

func (installer *linuxInstaller) activate(paths installerPaths) error {
	msbPath := filepath.Join(paths.binDir, "msb")
	if err := installer.run(msbPath, "--version"); err != nil {
		return fmt.Errorf("check MicroSandbox version: %w", err)
	}
	for _, permission := range []struct{ flag, name string }{{"-r", "read"}, {"-w", "write"}} {
		if err := installer.run("sudo", "test", permission.flag, "/dev/kvm"); err != nil {
			return fmt.Errorf("check /dev/kvm %s access: %w", permission.name, err)
		}
	}
	return nil
}

func (installer *linuxInstaller) run(name string, args ...string) error {
	cmd := exec.CommandContext(installer.ctx, name, args...)
	cmd.Stdout = installer.output.stdout
	cmd.Stderr = installer.output.stderr
	if err := installer.command(cmd); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func installerFileSHA256(path string) (_ string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func copyInstallerFile(source, destination string, mode os.FileMode) (err error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Chmod(mode), out.Close())
}

func replaceInstallerSymlink(path, target string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(target, path)
}

func appendInstallerPath(path, entry string) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(file, entry)
	return errors.Join(writeErr, file.Close())
}
