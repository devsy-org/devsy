package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type installRunner struct {
	calls []CommandSpec
	exit  int
}

func (r *installRunner) Run(_ context.Context, spec CommandSpec) CommandResult {
	r.calls = append(r.calls, spec)
	return CommandResult{ExitCode: &r.exit}
}

type installInfo struct{ fs.FileInfo }

type installWriter struct{ bytes.Buffer }

func (*installWriter) Close() error { return nil }

type installFS struct {
	files   map[string][]byte
	appends map[string]*installWriter
}

func (f *installFS) ReadFile(path string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return data, nil
}

func (f *installFS) Stat(path string) (fs.FileInfo, error) {
	if _, ok := f.files[path]; !ok {
		return nil, fs.ErrNotExist
	}
	return installInfo{}, nil
}

func (f *installFS) OpenAppend(path string) (io.WriteCloser, error) {
	w := &installWriter{}
	f.appends[path] = w
	return w, nil
}

type installFixture struct {
	deps      Dependencies
	cfg       InstallConfig
	runner    *installRunner
	files     *installFS
	out       *bytes.Buffer
	downloads int
	dir       string
	podman    string
}

func newInstallFixture(t *testing.T, msi []byte) *installFixture {
	t.Helper()
	dir := t.TempDir()
	fx := &installFixture{
		runner: &installRunner{},
		files:  &installFS{files: map[string][]byte{}, appends: map[string]*installWriter{}},
		out:    &bytes.Buffer{},
		dir:    dir,
	}
	fx.cfg = InstallConfig{
		URL:        "https://example.test/podman.msi",
		SHA256:     installSum(msi),
		MSIPath:    filepath.Join(dir, "podman.msi"),
		InstallDir: `C:\Program Files\RedHat\Podman`,
		LogPath:    filepath.Join(dir, "podman-install.log"),
		GitHubPath: filepath.Join(dir, "github_path"),
	}
	fx.podman = filepath.Join(fx.cfg.InstallDir, "podman.exe")
	fx.deps = Dependencies{
		Runner: fx.runner,
		Log:    &Logger{Out: fx.out, Err: fx.out},
		Files:  fx.files,
		Download: func(_ context.Context, _, dest string) error {
			fx.downloads++
			fx.files.files[dest] = msi
			return nil
		},
	}
	return fx
}

func (fx *installFixture) install() error {
	return installPodmanMSI(context.Background(), fx.deps, fx.cfg, fx.dir)
}

func numberedLines(n int) []byte {
	var lines []string
	for i := 1; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n")
}

func writeFiles(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstallPodmanMSISucceeds(t *testing.T) {
	msi := []byte("msi-bytes")
	fx := newInstallFixture(t, msi)
	fx.cfg.SHA256 = strings.ToUpper(installSum(msi))
	fx.files.files[fx.podman] = []byte{}

	if err := fx.install(); err != nil {
		t.Fatalf("installPodmanMSI() error = %v", err)
	}

	if fx.downloads != 1 {
		t.Fatalf("downloads = %d, want 1", fx.downloads)
	}
	if len(fx.runner.calls) != 1 {
		t.Fatalf("msiexec calls = %d, want 1", len(fx.runner.calls))
	}
	call := fx.runner.calls[0]
	if call.Path != "msiexec.exe" {
		t.Fatalf("msiexec path = %q, want msiexec.exe", call.Path)
	}
	wantArgs := []string{
		"/i", fx.cfg.MSIPath, "/qn", "/norestart", "/l*v", fx.cfg.LogPath,
		`INSTALLDIR="C:\Program Files\RedHat\Podman"`,
	}
	if !slices.Equal(call.Args, wantArgs) {
		t.Fatalf("msiexec args = %q, want %q", call.Args, wantArgs)
	}
	if got := fx.files.appends[fx.cfg.GitHubPath].String(); got != `C:\Program Files\RedHat\Podman`+"\n" {
		t.Fatalf("GITHUB_PATH content = %q", got)
	}
	if !strings.Contains(fx.out.String(), "MSI exit code: 0") {
		t.Fatalf("output %q missing msiexec exit code", fx.out.String())
	}
}

func TestInstallPodmanMSIRejectsChecksumMismatch(t *testing.T) {
	msi := []byte("msi-bytes")
	fx := newInstallFixture(t, msi)
	zeros := strings.Repeat("0", 64)
	fx.cfg.SHA256 = zeros

	err := fx.install()

	want := fmt.Sprintf(
		"SHA256 mismatch for podman MSI! Expected: %s, Got: %s",
		zeros,
		installSum(msi),
	)
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if len(fx.runner.calls) != 0 {
		t.Fatalf("msiexec ran despite checksum mismatch")
	}
	if len(fx.files.appends) != 0 {
		t.Fatalf("GITHUB_PATH written despite checksum mismatch")
	}
}

func TestInstallPodmanMSIMissingPodmanLogsLogTail(t *testing.T) {
	fx := newInstallFixture(t, []byte("msi-bytes"))
	fx.files.files[fx.cfg.LogPath] = numberedLines(60)

	err := fx.install()

	if err == nil || err.Error() != `podman.exe not found at C:\Program Files\RedHat\Podman` {
		t.Fatalf("error = %v", err)
	}
	out := fx.out.String()
	if !strings.Contains(out, "line 60\n") || !strings.Contains(out, "line 11\n") {
		t.Fatalf("output missing last 50 log lines:\n%s", out)
	}
	if strings.Contains(out, "line 10\n") {
		t.Fatalf("output includes more than the last 50 log lines:\n%s", out)
	}
	if len(fx.files.appends) != 0 {
		t.Fatalf("GITHUB_PATH written when podman.exe is missing")
	}
}

func TestInstallPodmanMSIMissingPodmanSearchesByDepth(t *testing.T) {
	fx := newInstallFixture(t, []byte("msi-bytes"))
	shallow := filepath.Join(fx.dir, "shallow", "podman.exe")
	deep := filepath.Join(fx.dir, "a", "b", "c", "d", "podman.exe")
	tooDeep := filepath.Join(fx.dir, "a", "b", "c", "d", "e", "podman.exe")
	writeFiles(t, shallow, deep, tooDeep)

	if err := fx.install(); err == nil {
		t.Fatal("installPodmanMSI() succeeded without podman.exe")
	}

	out := fx.out.String()
	if !strings.Contains(out, shallow) || !strings.Contains(out, deep) {
		t.Fatalf("output missing podman.exe search hits:\n%s", out)
	}
	if strings.Contains(out, tooDeep) {
		t.Fatalf("output includes podman.exe beyond search depth:\n%s", out)
	}
}

func TestInstallPodmanMSIReusesCachedMSI(t *testing.T) {
	msi := []byte("msi-bytes")
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{name: "verified cached MSI", data: msi},
		{name: "tampered cached MSI", data: []byte("tampered"), wantErr: "SHA256 mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fx := newInstallFixture(t, msi)
			fx.files.files[fx.cfg.MSIPath] = test.data
			fx.files.files[fx.podman] = []byte{}

			err := fx.install()

			if fx.downloads != 0 {
				t.Fatalf("downloads = %d, want 0 when MSI is cached", fx.downloads)
			}
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("error = %v, want nil", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Fatalf("error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestDownloadFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/podman.msi" {
			_, _ = w.Write([]byte("msi-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()

	dest := filepath.Join(dir, "podman.msi")
	if err := downloadFile(context.Background(), server.URL+"/podman.msi", dest); err != nil {
		t.Fatalf("downloadFile() error = %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "msi-bytes" {
		t.Fatalf("downloaded body = %q, want msi-bytes", got)
	}

	if err := downloadFile(
		context.Background(),
		server.URL+"/missing",
		filepath.Join(dir, "missing.msi"),
	); err == nil {
		t.Fatal("downloadFile() succeeded for HTTP 404")
	}
}
