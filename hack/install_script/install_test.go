package installscript

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var scriptPath = filepath.Join("..", "..", "sites", "docs-devsy-sh", "public", "install.sh")

const fakeBinary = "#!/bin/sh\necho 'devsy version v0.0.0-test'\n"

// fakeRelease serves release assets and, optionally, a goreleaser-style
// checksums.txt, recording the path of every asset request.
type fakeRelease struct {
	*httptest.Server
	requests *[]string
}

func newFakeRelease(t *testing.T, assets map[string]string, checksums map[string]string) *fakeRelease {
	t.Helper()
	requests := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		name := parts[len(parts)-1]
		if name == "checksums.txt" {
			if checksums == nil {
				http.NotFound(w, r)
				return
			}
			names := make([]string, 0, len(checksums))
			for n := range checksums {
				names = append(names, n)
			}
			sort.Strings(names)
			var b strings.Builder
			for _, n := range names {
				fmt.Fprintf(&b, "%s  %s\n", checksums[n], n)
			}
			_, _ = w.Write([]byte(b.String()))
			return
		}
		body, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		*requests = append(*requests, r.URL.Path)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &fakeRelease{Server: srv, requests: requests}
}

func checksumFor(body string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
}

func allAssets() map[string]string {
	return map[string]string{
		"devsy-linux-amd64":   fakeBinary,
		"devsy-linux-arm64":   fakeBinary,
		"devsy-darwin-amd64":  fakeBinary,
		"devsy-darwin-arm64":  fakeBinary,
		"devsy-windows-amd64": fakeBinary,
	}
}

// runInstall executes the install script with a clean DEVSY_*/FAKE_UNAME_*
// environment plus the given extra variables, returning stdout and stderr.
func runInstall(t *testing.T, extraEnv ...string) (string, string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is a POSIX sh script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}

	var env []string
	for _, kv := range os.Environ() {
		key := strings.SplitN(kv, "=", 2)[0]
		if strings.HasPrefix(key, "DEVSY_") || strings.HasPrefix(key, "FAKE_UNAME_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, extraEnv...)

	cmd := exec.Command("sh", scriptPath)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func installEnv(t *testing.T, baseURL string) []string {
	t.Helper()
	return []string{
		"DEVSY_INSTALL_DIR=" + t.TempDir(),
		"DEVSY_RELEASE_BASE_URL=" + baseURL,
	}
}

func TestInstallsLatestForHostPlatform(t *testing.T) {
	release := newFakeRelease(t, allAssets(), nil)
	env := installEnv(t, release.URL)
	installDir := strings.TrimPrefix(env[0], "DEVSY_INSTALL_DIR=")

	stdout, stderr, err := runInstall(t, env...)
	if err != nil {
		t.Fatalf("install failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	asset := fmt.Sprintf("devsy-%s-%s", runtime.GOOS, runtime.GOARCH)
	if len(*release.requests) != 1 || (*release.requests)[0] != "/latest/download/"+asset {
		t.Errorf("unexpected asset requests: %v", *release.requests)
	}

	installed := filepath.Join(installDir, "devsy")
	content, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	if string(content) != fakeBinary {
		t.Errorf("installed content mismatch: %q", content)
	}
	fi, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("installed mode = %o, want 755", fi.Mode().Perm())
	}
	if !strings.Contains(stdout, "devsy version v0.0.0-test") {
		t.Errorf("stdout missing installed version output:\n%s", stdout)
	}
}

func TestInstallsPinnedVersion(t *testing.T) {
	release := newFakeRelease(t, allAssets(), nil)
	env := append(installEnv(t, release.URL), "DEVSY_VERSION=v9.9.9")

	stdout, stderr, err := runInstall(t, env...)
	if err != nil {
		t.Fatalf("install failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	asset := fmt.Sprintf("devsy-%s-%s", runtime.GOOS, runtime.GOARCH)
	want := "/download/v9.9.9/" + asset
	if len(*release.requests) != 1 || (*release.requests)[0] != want {
		t.Errorf("requests = %v, want [%s]", *release.requests, want)
	}
}

func TestVerifiesPublishedChecksum(t *testing.T) {
	assets := allAssets()
	checksums := map[string]string{}
	for name, body := range assets {
		checksums[name] = checksumFor(body)
	}
	release := newFakeRelease(t, assets, checksums)

	stdout, stderr, err := runInstall(t, installEnv(t, release.URL)...)
	if err != nil {
		t.Fatalf("install failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "Checksum verified.") {
		t.Errorf("stdout missing checksum confirmation:\n%s", stdout)
	}
}

func TestChecksumMismatchAborts(t *testing.T) {
	assets := allAssets()
	checksums := map[string]string{}
	for name := range assets {
		checksums[name] = checksumFor("tampered")
	}
	release := newFakeRelease(t, assets, checksums)
	env := installEnv(t, release.URL)
	installDir := strings.TrimPrefix(env[0], "DEVSY_INSTALL_DIR=")

	_, stderr, err := runInstall(t, env...)
	if err == nil {
		t.Fatal("expected checksum mismatch to abort the install")
	}
	if !strings.Contains(stderr, "checksum mismatch") {
		t.Errorf("stderr missing mismatch diagnosis:\n%s", stderr)
	}
	if _, statErr := os.Stat(filepath.Join(installDir, "devsy")); !os.IsNotExist(statErr) {
		t.Error("binary was installed despite checksum mismatch")
	}
}

func TestMissingAssetFailsClearly(t *testing.T) {
	release := newFakeRelease(t, map[string]string{}, nil)

	_, stderr, err := runInstall(t, installEnv(t, release.URL)...)
	if err == nil {
		t.Fatal("expected missing asset to fail the install")
	}
	if !strings.Contains(stderr, "download failed") {
		t.Errorf("stderr missing download failure diagnosis:\n%s", stderr)
	}
}

// withFakeUname prepends a shimmed uname to PATH so platform detection can
// be exercised for platforms other than the test host.
func withFakeUname(t *testing.T, kernel, machine string) []string {
	t.Helper()
	dir := t.TempDir()
	shim := "#!/bin/sh\ncase \"$1\" in\n" +
		"-s) printf '%s\\n' \"$FAKE_UNAME_S\" ;;\n" +
		"-m) printf '%s\\n' \"$FAKE_UNAME_M\" ;;\n" +
		"*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_UNAME_S=" + kernel,
		"FAKE_UNAME_M=" + machine,
	}
}

func TestPlatformDetection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kernel    string
		machine   string
		wantAsset string // empty: expect a clear failure instead
		wantErr   string
	}{
		{"linux arm64", "Linux", "aarch64", "devsy-linux-arm64", ""},
		{"macOS intel", "Darwin", "x86_64", "devsy-darwin-amd64", ""},
		{"macOS silicon", "Darwin", "arm64", "devsy-darwin-arm64", ""},
		{"windows shell", "MINGW64_NT-10.0", "x86_64", "", "for Windows see"},
		{"unsupported OS", "FreeBSD", "amd64", "", "unsupported operating system"},
		{"unsupported arch", "Linux", "riscv64", "", "unsupported CPU architecture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := newFakeRelease(t, allAssets(), nil)
			env := append(installEnv(t, release.URL), withFakeUname(t, tc.kernel, tc.machine)...)

			_, stderr, err := runInstall(t, env...)
			if tc.wantAsset == "" {
				if err == nil {
					t.Fatal("expected install to fail on this platform")
				}
				if !strings.Contains(stderr, tc.wantErr) {
					t.Errorf("stderr missing %q:\n%s", tc.wantErr, stderr)
				}
				return
			}
			if err != nil {
				t.Fatalf("install failed: %v\nstderr: %s", err, stderr)
			}
			if len(*release.requests) != 1 || (*release.requests)[0] != "/latest/download/"+tc.wantAsset {
				t.Errorf("requests = %v, want asset %s", *release.requests, tc.wantAsset)
			}
		})
	}
}

func TestScriptPassesDashSyntaxCheck(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if out, err := exec.Command("sh", "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
}
