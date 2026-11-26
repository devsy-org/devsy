package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const (
	setupWantDaemon = `{
  "insecure-registries": [
    "host.docker.internal:15500"
  ],
  "log-level": "warn"
}
`
	setupWantHostsLine = "127.0.0.1 host.docker.internal\n"
	setupEmptyDaemon   = `{
  "insecure-registries": [
    "host.docker.internal:15500"
  ]
}
`
	setupSortedDaemon = `{
  "insecure-registries": [
    "alpha:5000",
    "host.docker.internal:15500",
    "zeta:5000"
  ]
}
`
	setupOtherKeysDaemon = `{
  "features": {
    "buildkit": true
  },
  "insecure-registries": [
    "host.docker.internal:15500"
  ],
  "max-concurrent-downloads": 3
}
`
	setupNullDoc   = "null"
	setupPodmanCmd = "podman-linux"
	setupHostCmd   = "host"
	setupBogusFlag = "--bogus"
	setupDockerDir = "docker"
	setupLabelFlag = "--label"
)

// setupFakeRunner records every command instead of running it. The first
// infoFails docker info calls fail.
type setupFakeRunner struct {
	calls     []setupCall
	infoFails int
	infoCalls int
}

type setupCall struct {
	cmd   string
	stdin string
}

func (r *setupFakeRunner) Run(_ context.Context, spec commandSpec) error {
	call := setupCall{cmd: strings.Join(append([]string{spec.Name}, spec.Args...), " ")}
	if spec.Stdin != nil {
		data, err := io.ReadAll(spec.Stdin)
		if err != nil {
			return err
		}
		call.stdin = string(data)
	}
	r.calls = append(r.calls, call)
	if spec.Name != dockerBinary {
		return nil
	}
	r.infoCalls++
	if r.infoCalls <= r.infoFails {
		return errors.New("cannot connect to the docker daemon")
	}
	return nil
}

func (r *setupFakeRunner) commands() []string {
	out := make([]string, 0, len(r.calls))
	for _, call := range r.calls {
		out = append(out, call.cmd)
	}
	return out
}

// setupClock is a fake clock whose sleep advances time.
type setupClock struct {
	t      time.Time
	sleeps int
}

func (c *setupClock) now() time.Time { return c.t }

func (c *setupClock) sleep(d time.Duration) {
	c.t = c.t.Add(d)
	c.sleeps++
}

// setupTarEntry describes one entry of a generated archive fixture.
type setupTarEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
	linkname string
}

func buildSetupArchive(t testing.TB, entries []setupTarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     e.mode,
			Linkname: e.linkname,
			Size:     int64(len(e.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// podmanEntries is a podman-static like archive. Symlinks are left out on
// Windows, where creating them needs privileges.
func podmanEntries() []setupTarEntry {
	entries := []setupTarEntry{
		{name: "podman-linux-amd64/", typeflag: tar.TypeDir, mode: 0o755},
		{
			name:     "podman-linux-amd64/usr/bin/podman",
			typeflag: tar.TypeReg,
			mode:     0o755,
			body:     "podman-binary",
		},
		{
			name:     "podman-linux-amd64/etc/containers/policy.json",
			typeflag: tar.TypeReg,
			mode:     0o644,
			body:     "{}",
		},
	}
	if runtime.GOOS != osWindows {
		entries = append(entries, setupTarEntry{
			name:     "podman-linux-amd64/usr/bin/docker",
			typeflag: tar.TypeSymlink,
			mode:     0o777,
			linkname: "podman",
		})
	}
	return entries
}

func setupSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type setupSuite struct {
	suite.Suite
	hosts   string
	daemon  string
	archive string
	extract string
	runner  *setupFakeRunner
	clock   *setupClock
	start   time.Time
	h       *hostSetup
}

func (s *setupSuite) SetupTest() {
	dir := s.T().TempDir()
	s.hosts = filepath.Join(dir, "hosts")
	s.daemon = filepath.Join(dir, setupDockerDir, "daemon.json")
	s.archive = filepath.Join(dir, "podman-linux-amd64.tar.gz")
	s.extract = filepath.Join(dir, "extract")
	s.Require().NoError(os.WriteFile(s.hosts, []byte("127.0.0.1 localhost\n"), 0o600))
	s.runner = &setupFakeRunner{}
	s.start = time.Unix(1_000_000, 0)
	s.clock = &setupClock{t: s.start}
	s.h = &hostSetup{
		runner:     s.runner,
		goos:       osLinux,
		hostsFile:  s.hosts,
		daemonFile: s.daemon,
		stdout:     io.Discard,
		stderr:     io.Discard,
		now:        s.clock.now,
		sleep:      s.clock.sleep,
	}
}

func (s *setupSuite) TestMergeInsecureRegistry() {
	tests := []struct {
		name     string
		existing string
		want     string
		wantErr  bool
	}{
		{name: "empty input", existing: "", want: setupEmptyDaemon},
		{name: "whitespace only", existing: " \n\t", want: setupEmptyDaemon},
		{name: "null document", existing: setupNullDoc, want: setupEmptyDaemon},
		{
			name:     "missing key keeps other keys",
			existing: `{"log-level":"warn"}`,
			want:     setupWantDaemon,
		},
		{
			name:     "existing entries are sorted",
			existing: `{"insecure-registries":["zeta:5000","alpha:5000"]}`,
			want:     setupSortedDaemon,
		},
		{
			name:     "registry already present is not duplicated",
			existing: `{"insecure-registries":["host.docker.internal:15500","host.docker.internal:15500"]}`,
			want:     setupEmptyDaemon,
		},
		{
			name:     "other keys are preserved",
			existing: `{"max-concurrent-downloads":3,"features":{"buildkit":true}}`,
			want:     setupOtherKeysDaemon,
		},
		{name: "invalid JSON", existing: `{"log-level":`, wantErr: true},
		{
			name:     "insecure-registries not a list",
			existing: `{"insecure-registries":"a:5000"}`,
			wantErr:  true,
		},
		{name: "non-string entry", existing: `{"insecure-registries":[1]}`, wantErr: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := mergeInsecureRegistry([]byte(tt.existing), snapshotRegistry)
			if tt.wantErr {
				s.ErrorIs(err, errInvalidDaemon)
				return
			}
			s.Require().NoError(err)
			s.Equal(tt.want, string(got))
		})
	}
}

func (s *setupSuite) TestRemoveDockerRunsCommandsInOrder() {
	s.Require().NoError(s.h.apply(context.Background(), []string{actionRemoveDocker}))
	s.Equal([]string{
		"sudo systemctl stop docker.socket docker.service",
		"sudo apt-get remove -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin",
		"sudo rm -rf /var/lib/docker /var/lib/containerd /usr/bin/docker /usr/bin/dockerd",
	}, s.runner.commands())
}

func (s *setupSuite) TestInsecureRegistryFullSequence() {
	s.Require().NoError(os.MkdirAll(filepath.Dir(s.daemon), 0o755))
	s.Require().NoError(os.WriteFile(s.daemon, []byte(`{"log-level":"warn"}`), 0o600))

	s.Require().NoError(s.h.apply(context.Background(), []string{actionInsecureRegistry}))

	s.Equal([]string{
		"sudo tee -a " + s.hosts,
		"sudo mkdir -p " + filepath.Dir(s.daemon),
		"sudo tee " + s.daemon,
		"sudo systemctl restart docker",
		"docker info",
	}, s.runner.commands())
	s.Equal(setupWantHostsLine, s.runner.calls[0].stdin)
	s.Equal(setupWantDaemon, s.runner.calls[2].stdin)
	s.Equal(0, s.clock.sleeps)
}

func (s *setupSuite) TestHostsEntryAddedOnlyWhenMissing() {
	tests := []struct {
		name    string
		hosts   string
		wantTee bool
	}{
		{name: "present", hosts: "127.0.0.1 localhost host.docker.internal\n"},
		{name: "commented out", hosts: "# 127.0.0.1 host.docker.internal\n", wantTee: true},
		{name: "missing", hosts: "127.0.0.1 localhost\n", wantTee: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.runner.calls = nil
			s.Require().NoError(os.WriteFile(s.hosts, []byte(tt.hosts), 0o600))

			s.Require().NoError(s.h.addHostsEntry(context.Background()))

			s.Equal(tt.wantTee, slices.Contains(s.runner.commands(), "sudo tee -a "+s.hosts))
		})
	}
}

func (s *setupSuite) TestWaitForDockerTimesOut() {
	s.runner.infoFails = 1000

	err := s.h.apply(context.Background(), []string{actionInsecureRegistry})

	s.ErrorIs(err, errDockerNotReady)
	s.Equal(31, s.runner.infoCalls)
	s.Equal(30*time.Second, s.clock.t.Sub(s.start))
}

func (s *setupSuite) TestWaitForDockerSucceedsAfterRetries() {
	s.runner.infoFails = 3

	s.Require().NoError(s.h.apply(context.Background(), []string{actionInsecureRegistry}))

	s.Equal(4, s.runner.infoCalls)
	s.Equal(3, s.clock.sleeps)
	s.Equal(3*time.Second, s.clock.t.Sub(s.start))
}

func (s *setupSuite) TestUnknownActionRunsNothing() {
	err := s.h.apply(context.Background(), []string{actionRemoveDocker, "reboot-the-world"})

	s.ErrorIs(err, errUnknownAction)
	s.Empty(s.runner.calls)
}

func (s *setupSuite) TestNonLinuxHostIsRejected() {
	s.h.goos = "darwin"

	s.ErrorIs(s.h.apply(context.Background(), []string{actionRemoveDocker}), errNotLinux)
	s.Empty(s.runner.calls)
	s.NoError(s.h.apply(context.Background(), nil))
}

func (s *setupSuite) TestPodmanInstallExtractsAndCopies() {
	data := buildSetupArchive(s.T(), podmanEntries())
	s.writeArchive(data)
	p := s.newPodman("", setupSHA(data), true)

	s.Require().NoError(p.install(context.Background()))

	src := filepath.Join(s.extract, podmanPackageDir)
	body, err := os.ReadFile(filepath.Join(src, "usr", "bin", "podman"))
	s.Require().NoError(err)
	s.Equal("podman-binary", string(body))
	if runtime.GOOS != osWindows {
		info, err := os.Stat(filepath.Join(src, "usr", "bin", "podman"))
		s.Require().NoError(err)
		s.Equal(os.FileMode(0o755), info.Mode().Perm())
		target, err := os.Readlink(filepath.Join(src, "usr", "bin", setupDockerDir))
		s.Require().NoError(err)
		s.Equal("podman", target)
	}
	s.Equal([]string{
		"sudo cp -r " + filepath.Join(src, "usr") + " " + filepath.Join(src, "etc") + " /",
	}, s.runner.commands())
}

func (s *setupSuite) TestPodmanChecksumMismatchIsReported() {
	data := buildSetupArchive(s.T(), podmanEntries())
	s.writeArchive(data)
	wrong := strings.Repeat("0", sha256.Size*2)

	p := s.newPodman("", wrong, true)
	err := p.install(context.Background())

	s.EqualError(err, fmt.Sprintf(
		"SHA256 mismatch for podman-static archive! Expected: %s, Got: %s", wrong, setupSHA(data)))
	s.Empty(s.runner.calls)
	s.NoDirExists(s.extract)
}

func (s *setupSuite) TestPodmanCLIReportsChecksumMismatchAsAnnotation() {
	data := buildSetupArchive(s.T(), podmanEntries())
	s.writeArchive(data)
	wrong := strings.Repeat("f", sha256.Size*2)
	var stderr bytes.Buffer

	code := setupCLI([]string{
		setupPodmanCmd,
		"--archive", s.archive,
		"--url", "http://127.0.0.1:1/unused",
		"--sha256", wrong,
		"--extract-dir", s.extract,
	}, io.Discard, &stderr)

	s.Equal(1, code)
	s.Contains(
		stderr.String(),
		"::error::SHA256 mismatch for podman-static archive! Expected: "+wrong,
	)
}

func (s *setupSuite) TestPodmanDownloadsWhenArchiveAbsent() {
	data := buildSetupArchive(s.T(), podmanEntries())
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(data)
	}))
	defer server.Close()
	p := s.newPodman(server.URL+"/podman-linux-amd64.tar.gz", setupSHA(data), true)
	p.client = server.Client()

	s.Require().NoError(p.install(context.Background()))

	s.Equal(int32(1), hits.Load())
	s.FileExists(s.archive)
	s.NoFileExists(s.archive + partialSuffix)
	s.FileExists(filepath.Join(s.extract, podmanPackageDir, "usr", "bin", "podman"))
}

func (s *setupSuite) TestPodmanDownloadFailsOnErrorStatus() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer server.Close()
	p := s.newPodman(server.URL+"/missing.tar.gz", strings.Repeat("0", sha256.Size*2), true)
	p.client = server.Client()

	err := p.install(context.Background())

	s.ErrorContains(err, "404")
	s.NoFileExists(s.archive)
	s.Empty(s.runner.calls)
}

func (s *setupSuite) TestPodmanRejectsEscapingEntries() {
	tests := []struct {
		name    string
		entries []setupTarEntry
	}{
		{
			name: "parent traversal",
			entries: []setupTarEntry{
				{name: "../escape.txt", typeflag: tar.TypeReg, mode: 0o644, body: "x"},
			},
		},
		{
			name: "traversal below the package directory",
			entries: []setupTarEntry{
				{
					name:     "podman-linux-amd64/../../escape.txt",
					typeflag: tar.TypeReg,
					mode:     0o644,
					body:     "x",
				},
			},
		},
		{
			name: "absolute name",
			entries: []setupTarEntry{
				{
					name:     filepath.Join(string(filepath.Separator), "escape.txt"),
					typeflag: tar.TypeReg,
					mode:     0o644,
				},
			},
		},
		{
			name: "symlink pointing outside",
			entries: []setupTarEntry{{
				name:     "podman-linux-amd64/usr/bin/evil",
				typeflag: tar.TypeSymlink,
				mode:     0o777,
				linkname: "../../../../escape.txt",
			}},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			if runtime.GOOS == osWindows && strings.Contains(tt.name, "symlink") {
				s.T().Skip("creating symlinks needs privileges on Windows")
			}
			s.runner.calls = nil
			data := buildSetupArchive(s.T(), tt.entries)
			s.writeArchive(data)
			p := s.newPodman("", setupSHA(data), true)

			err := p.install(context.Background())

			s.ErrorIs(err, errEscapesDest)
			s.NoFileExists(filepath.Join(filepath.Dir(s.extract), "escape.txt"))
			s.Empty(s.runner.calls)
		})
	}
}

func (s *setupSuite) TestUIDMapInstalledOnlyWhenMissing() {
	data := buildSetupArchive(s.T(), podmanEntries())
	s.writeArchive(data)
	aptInstall := "sudo apt-get install -y uidmap"

	s.Run("present", func() {
		s.runner.calls = nil
		s.Require().NoError(s.newPodman("", setupSHA(data), true).install(context.Background()))
		s.NotContains(s.runner.commands(), aptInstall)
	})
	s.Run("missing", func() {
		s.runner.calls = nil
		s.Require().NoError(s.newPodman("", setupSHA(data), false).install(context.Background()))
		s.Equal([]string{
			"sudo cp -r " + filepath.Join(s.extract, podmanPackageDir, "usr") + " " +
				filepath.Join(s.extract, podmanPackageDir, "etc") + " /",
			aptInstall,
		}, s.runner.commands())
	})
}

func (s *setupSuite) TestUsageErrorsExitTwo() {
	tests := [][]string{
		nil,
		{"unknown"},
		{setupHostCmd},
		{setupHostCmd, setupLabelFlag, "context", setupBogusFlag},
		{setupPodmanCmd},
		{setupPodmanCmd, "--archive", s.archive},
	}
	for _, args := range tests {
		s.Equal(2, setupCLI(args, io.Discard, io.Discard), "args %v", args)
	}
}

func (s *setupSuite) TestHostCLIRunsNothingForEmptyHostSetup() {
	s.Equal(0, setupCLI([]string{setupHostCmd, setupLabelFlag, "context"}, io.Discard, io.Discard))
}

func (s *setupSuite) TestHostCLIFailsForUnknownLabel() {
	s.Equal(
		1,
		setupCLI([]string{setupHostCmd, setupLabelFlag, "no-such-suite"}, io.Discard, io.Discard),
	)
}

func (s *setupSuite) newPodman(url, sum string, uidmapPresent bool) *podmanInstall {
	return &podmanInstall{
		archive:    s.archive,
		url:        url,
		sha256Hex:  sum,
		extractDir: s.extract,
		client:     http.DefaultClient,
		runner:     s.runner,
		lookPath: func(string) (string, error) {
			if uidmapPresent {
				return "/usr/bin/newuidmap", nil
			}
			return "", exec.ErrNotFound
		},
		stdout: io.Discard,
		stderr: io.Discard,
	}
}

func (s *setupSuite) writeArchive(data []byte) {
	s.Require().NoError(os.WriteFile(s.archive, data, 0o600))
}

func TestCISetupSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(setupSuite))
}
