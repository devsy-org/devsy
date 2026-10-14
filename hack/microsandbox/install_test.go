package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	installerTarCommand  = "tar"
	installerBinaryName  = "msb"
	installerSudoCommand = "sudo"
	installerTestCommand = "test"
	installerTestURL     = "https://github.com/superradcompany/microsandbox/releases/download/" +
		"v0.7.7/microsandbox-linux-x86_64.tar.gz"
)

func TestInstallLinuxPinnedBundle(t *testing.T) {
	require.Equal(t, "v0.7.7", microsandboxVersion)
	require.Equal(t, installerTestURL, microsandboxURL)
	require.Equal(
		t,
		"b3cc4a5e3f52dfdd938a6f67ac4a9a959ddfe304bab56de4964044b8613f01bb",
		microsandboxSHA256,
	)
	require.Equal(t, "microsandbox-linux-x86_64-v0.7.7.tar.gz", microsandboxArchive)
}

func TestInstallLinuxBundleUsesCachedArchiveAndInstallsFiles(t *testing.T) {
	fixture := newInstallerFixture(t)
	archive := installerTarFixture(t)
	fixture.cacheArchive(t, archive)

	runner := &installerTestRunner{fixture: fixture}
	var stdout, stderr bytes.Buffer
	require.NoError(t, fixture.installer(context.Background(), runner.run,
		commandOutput{stdout: &stdout, stderr: &stderr}).install(), stderr.String())
	require.Equal(t, "MicroSandbox v0.7.7\n", stdout.String())
	require.Equal(
		t,
		[]string{
			installerTarCommand,
			installerBinaryName,
			installerSudoCommand,
			installerSudoCommand,
		},
		runner.names,
	)
	installerAssertInstalled(t, fixture)
	installerAssertOnlyCachedArchive(t, fixture.runnerTemp, fixture.bundle.filename)
}

func TestInstallLinuxBundleDownloadsPinnedArchiveBeforeExtraction(t *testing.T) {
	fixture := newInstallerFixture(t)
	runner := &installerTestRunner{fixture: fixture, archive: installerTarFixture(t)}
	fixture.bundle.sha256 = installerDigest(runner.archive)
	runner.fixture = fixture
	require.NoError(t, fixture.installer(context.Background(), runner.run,
		commandOutput{stdout: io.Discard, stderr: io.Discard}).install())
	require.Equal(
		t,
		[]string{
			"curl",
			installerTarCommand,
			installerBinaryName,
			installerSudoCommand,
			installerSudoCommand,
		},
		runner.names,
	)
	require.True(t, runner.downloaded)
	installerAssertInstalled(t, fixture)
}

func TestInstallLinuxBundleRejectsBadChecksumBeforeExtraction(t *testing.T) {
	fixture := newInstallerFixture(t)
	fixture.cacheArchive(t, installerTarFixture(t))
	fixture.bundle.sha256 = strings.Repeat("0", sha256.Size*2)
	runner := &installerTestRunner{fixture: fixture}

	err := fixture.installer(context.Background(), runner.run,
		commandOutput{stdout: io.Discard, stderr: io.Discard}).install()
	require.ErrorContains(t, err, "checksum mismatch")
	require.Empty(t, runner.names)
	_, err = os.Stat(filepath.Join(fixture.home, ".microsandbox"))
	require.ErrorIs(t, err, os.ErrNotExist)
	installerAssertOnlyCachedArchive(t, fixture.runnerTemp, fixture.bundle.filename)
}

func TestInstallLinuxBundlePreservesKVMCommandFailure(t *testing.T) {
	fixture := newInstallerFixture(t)
	fixture.cacheArchive(t, installerTarFixture(t))
	failure := errors.New("sudo test exited 23")
	runner := &installerTestRunner{fixture: fixture, failKVMWrite: failure}

	err := fixture.installer(context.Background(), runner.run,
		commandOutput{stdout: io.Discard, stderr: io.Discard}).install()
	require.ErrorIs(t, err, failure)
	require.Equal(
		t,
		[]string{
			installerTarCommand,
			installerBinaryName,
			installerSudoCommand,
			installerSudoCommand,
		},
		runner.names,
	)
	installerAssertInstalled(t, fixture)
}

func TestInstallLinuxBundleCleansStagingWhenExtractionFails(t *testing.T) {
	fixture := newInstallerFixture(t)
	fixture.cacheArchive(t, installerTarFixture(t))
	failure := errors.New("tar exited 17")
	runner := &installerTestRunner{fixture: fixture, failExtraction: failure}

	err := fixture.installer(context.Background(), runner.run,
		commandOutput{stdout: io.Discard, stderr: io.Discard}).install()
	require.ErrorIs(t, err, failure)
	installerAssertOnlyCachedArchive(t, fixture.runnerTemp, fixture.bundle.filename)
}

type installerTestFixture struct {
	runnerTemp string
	home       string
	githubPath string
	bundle     installerBundle
}

func newInstallerFixture(t *testing.T) installerTestFixture {
	t.Helper()
	runnerTemp := t.TempDir()
	home := t.TempDir()
	githubPath := filepath.Join(t.TempDir(), "github-path")
	require.NoError(t, os.WriteFile(githubPath, nil, 0o600))
	t.Setenv("RUNNER_TEMP", runnerTemp)
	t.Setenv("HOME", home)
	t.Setenv("GITHUB_PATH", githubPath)
	return installerTestFixture{
		runnerTemp: runnerTemp,
		home:       home,
		githubPath: githubPath,
		bundle: installerBundle{
			filename: microsandboxArchive,
			url:      "https://example.invalid/microsandbox.tar.gz",
		},
	}
}

func (fixture installerTestFixture) installer(
	ctx context.Context,
	run commandRunner,
	output commandOutput,
) *linuxInstaller {
	return &linuxInstaller{ctx: ctx, command: run, output: output, bundle: fixture.bundle}
}

func (fixture *installerTestFixture) cacheArchive(t *testing.T, archive []byte) {
	t.Helper()
	path := filepath.Join(fixture.runnerTemp, fixture.bundle.filename)
	require.NoError(t, os.WriteFile(path, archive, 0o600))
	fixture.bundle.sha256 = installerDigest(archive)
}

type installerTestRunner struct {
	fixture        installerTestFixture
	archive        []byte
	failExtraction error
	failKVMWrite   error
	names          []string
	downloaded     bool
}

func (runner *installerTestRunner) run(cmd *exec.Cmd) error {
	name := filepath.Base(cmd.Args[0])
	runner.names = append(runner.names, name)
	if name == "curl" {
		return runner.downloadArchive(cmd.Args[1:])
	}
	if runner.failExtraction != nil && name == installerTarCommand {
		return runner.failExtraction
	}
	switch name {
	case installerTarCommand:
		return installerExtractFixture(cmd.Args[1:])
	case installerBinaryName:
		_, _ = io.WriteString(cmd.Stdout, "MicroSandbox v0.7.7\n")
	case installerSudoCommand:
		return runner.checkKVM(cmd.Args[1:])
	default:
		return errors.New("unexpected installer command: " + name)
	}
	return nil
}

func (runner *installerTestRunner) downloadArchive(args []string) error {
	want := []string{
		"-fsSL", "--retry", "3", "-o",
		filepath.Join(runner.fixture.runnerTemp, runner.fixture.bundle.filename),
		runner.fixture.bundle.url,
	}
	if len(args) != len(want) {
		return errors.New("unexpected curl arguments")
	}
	for index := range want {
		if args[index] != want[index] {
			return errors.New("unexpected curl arguments")
		}
	}
	runner.downloaded = true
	return os.WriteFile(want[4], runner.archive, 0o600)
}

func (runner *installerTestRunner) checkKVM(args []string) error {
	if len(args) != 3 || args[0] != installerTestCommand ||
		(args[1] != "-r" && args[1] != "-w") || args[2] != "/dev/kvm" {
		return errors.New("unexpected KVM check arguments")
	}
	if args[1] == "-w" {
		return runner.failKVMWrite
	}
	return nil
}

func installerTarFixture(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := tar.NewWriter(&data)
	for _, entry := range []struct {
		name string
		mode int64
		body string
	}{
		{name: installerBinaryName, mode: 0o755, body: "#!/bin/sh\necho MicroSandbox v0.7.7\n"},
		{name: microsandboxLibrary, mode: 0o644, body: "library fixture"},
	} {
		require.NoError(t, writer.WriteHeader(&tar.Header{
			Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: tar.TypeReg,
		}))
		_, err := io.WriteString(writer, entry.body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return data.Bytes()
}

func installerDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func installerExtractFixture(args []string) error {
	if len(args) != 4 || args[0] != "-xzf" || args[2] != "-C" {
		return errors.New("unexpected tar arguments")
	}
	staging := args[3]
	msb := filepath.Join(staging, installerBinaryName)
	content := []byte("#!/bin/sh\necho MicroSandbox v0.7.7\n")
	if err := os.WriteFile(msb, content, 0o600); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(staging, microsandboxLibrary),
		[]byte("library fixture"),
		0o600,
	)
}

func installerAssertInstalled(t *testing.T, fixture installerTestFixture) {
	t.Helper()
	msb := filepath.Join(fixture.home, ".microsandbox", "bin", installerBinaryName)
	library := filepath.Join(fixture.home, ".microsandbox", "lib", microsandboxLibrary)
	msbInfo, err := os.Stat(msb)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), msbInfo.Mode().Perm())
	libraryInfo, err := os.Stat(library)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), libraryInfo.Mode().Perm())
	installerAssertLinks(t, fixture.home, msb)
	pathBytes, err := os.ReadFile(fixture.githubPath)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(fixture.home, ".local", "bin")+"\n", string(pathBytes))
}

func installerAssertLinks(t *testing.T, home, msb string) {
	t.Helper()
	for _, link := range []struct{ name, target string }{
		{name: filepath.Join(home, ".microsandbox", "lib", "libkrunfw.so.5"), target: microsandboxLibrary},
		{name: filepath.Join(home, ".microsandbox", "lib", "libkrunfw.so"), target: "libkrunfw.so.5"},
		{name: filepath.Join(home, ".local", "bin", installerBinaryName), target: msb},
	} {
		got, err := os.Readlink(link.name)
		require.NoError(t, err)
		require.Equal(t, link.target, got)
	}
}

func installerAssertOnlyCachedArchive(t *testing.T, dir, archive string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, archive, entries[0].Name())
}
