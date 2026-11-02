package ssh

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework/ghfixture"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/gh-relay/Dockerfile
var fixtureFiles embed.FS

func testGitHubCLIForwarding(ctx context.Context) {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	fixture := prepareFixture(t, ctx, dir)
	prepareHostAuthentication(t, dir)
	stop, done := startFixture(t, ctx, dir, fixture)
	defer stop()
	waitFixture(t, ctx, fixture.name, done)
	// stdout contains only boolean test results. Raw gh streams and credentials
	// stay inside the disposable fixture and are never attached to failures.
	//nolint:gosec // name is the generated test container.
	check := exec.CommandContext(
		ctx,
		"docker",
		"exec",
		fixture.name,
		"/fixture/ghfixture",
		"check",
	)
	report, err := check.CombinedOutput()

	require.NoError(t, err, "real gh checks: %s", report)
	verifyRealGHReport(t, report)

	verifyRelayFallback(t, ctx, fixture.name, stop)
	//nolint:gosec // The container name belongs to this generated, isolated fixture.
	after := exec.CommandContext(
		ctx,
		"docker",
		"exec",
		fixture.name,
		"/fixture/ghfixture",
		"after-relay",
	)
	proof, err := after.CombinedOutput()
	require.NoError(t, err, "relay-stopped checks: %s", proof)
	_, err = ghfixture.ValidateReport(proof, ghfixture.ExpectedRelayLossChecks)
	require.NoError(t, err)
}

func downloadGH(t ginkgo.GinkgoTInterface, ctx context.Context, dir, arch string) {
	t.Helper()
	const version = "2.101.0"
	filename := "gh_" + version + "_linux_" + arch + ".tar.gz"
	get := func(name string) []byte {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"https://github.com/cli/cli/releases/download/v"+version+"/"+name,
			nil,
		)
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = response.Body.Close() }()
		require.Equal(t, http.StatusOK, response.StatusCode)
		b, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
		require.NoError(t, err)
		return b
	}
	archive := get(filename)
	sums := string(get("gh_" + version + "_checksums.txt"))
	hash := sha256.Sum256(archive)
	require.Contains(t, sums, fmt.Sprintf("%x  %s", hash, filename))
	z, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	defer func() { _ = z.Close() }()
	tarReader := tar.NewReader(z)
	for {
		header, err := tarReader.Next()
		require.NoError(t, err)
		if strings.HasSuffix(header.Name, "/bin/gh") {
			binary, err := io.ReadAll(io.LimitReader(tarReader, 128<<20))
			require.NoError(t, err)
			// #nosec G306 -- Executable release binary, verified against its published checksum.
			require.NoError(t, os.WriteFile(filepath.Join(dir, "gh"), binary, 0o755))
			return
		}
	}
}

func verifyRealGHReport(t ginkgo.GinkgoTInterface, report []byte) {
	t.Helper()
	results, err := ghfixture.ValidateReport(report, ghfixture.ExpectedChecks)
	require.NoError(t, err)
	for _, r := range results.Checks {
		t.Log(r.Name)
	}
	t.Logf("guest CLI: %s; guest architecture: %s", results.CLI, results.Architecture)
}

type ghRelayFixture struct {
	name string
	arch string
}

func prepareFixture(t ginkgo.GinkgoTInterface, ctx context.Context, dir string) ghRelayFixture {
	t.Helper()
	agents, err := os.OpenRoot("bin")
	require.NoError(t, err)
	defer func() { _ = agents.Close() }()
	arch := fixtureArchitecture(t, ctx)
	agent := "devsy-linux-" + arch
	data, err := agents.ReadFile(agent)
	require.NoError(t, err)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	require.NoError(t, root.WriteFile("devsy", data, 0o755))
	fixtureBinary, err := agents.ReadFile("ghfixture-linux-" + arch)
	require.NoError(t, err)
	require.NoError(t, root.WriteFile("ghfixture", fixtureBinary, 0o755))
	for _, name := range []string{"Dockerfile"} {
		b, err := fixtureFiles.ReadFile("testdata/gh-relay/" + name)
		require.NoError(t, err)
		require.NoError(t, root.WriteFile(name, b, 0o600))
	}
	random := make([]byte, 12)
	_, err = rand.Read(random)
	require.NoError(t, err)
	name := "devsy-gh-test-" + hex.EncodeToString(random)
	image := name + ":test"
	downloadGH(t, ctx, dir, arch)
	//nolint:gosec // image and directory are generated fixture resources.
	buildImage := exec.CommandContext(
		ctx,
		"docker",
		"build",
		"--platform",
		"linux/"+arch,
		"-q",
		"-t",
		image,
		dir,
	)
	imageOut, err := buildImage.CombinedOutput()
	require.NoError(t, err, "fixture image: %s", imageOut)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		//nolint:gosec // image is the random, test-owned tag.
		_ = exec.CommandContext(cleanup, "docker", "image", "rm", image).Run()
	})
	return ghRelayFixture{name: name, arch: arch}
}

func fixtureArchitecture(t ginkgo.GinkgoTInterface, ctx context.Context) string {
	t.Helper()
	info := exec.CommandContext(ctx, "docker", "info", "--format", "{{.OSType}} {{.Architecture}}")
	data, err := info.Output()
	require.NoError(t, err)
	fields := strings.Fields(string(data))
	require.Len(t, fields, 2)
	require.Equal(t, "linux", fields[0], "GitHub forwarding fixtures require a Linux Docker engine")
	arch := fields[1]
	switch arch {
	case "aarch64":
		arch = "arm64"
	case "x86_64":
		arch = "amd64"
	}
	require.Contains(t, []string{"arm64", "amd64"}, arch, "unsupported Docker engine architecture")
	t.Logf("native host: %s/%s; native Docker guest: linux/%s", runtime.GOOS, runtime.GOARCH, arch)
	return arch
}

func prepareHostAuthentication(t ginkgo.GinkgoTInterface, dir string) {
	t.Helper()
	type tokens struct {
		Forwarded string `json:"forwarded"`
		Local     string `json:"local"`
		Second    string `json:"second"`
		Invalid   string `json:"invalid"`
		Refresh   string `json:"refresh"`
	}
	accounts := map[string]tokens{}
	var hosts strings.Builder
	for _, host := range []string{"github.com", "enterprise.one.test", "enterprise.two.test"} {
		token := func() string {
			b := make([]byte, 20)
			_, err := rand.Read(b)
			require.NoError(t, err)
			return "ghp_" + hex.EncodeToString(b)
		}
		v := tokens{
			Forwarded: token(),
			Local:     token(),
			Second:    token(),
			Invalid:   token(),
			Refresh:   token(),
		}
		accounts[host] = v
		fmt.Fprintf(
			&hosts,
			"%s:\n    user: host-user\n    oauth_token: %s\n",
			host,
			v.Forwarded,
		)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "host-gh"), 0o700))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "host-gh", "hosts.yml"), []byte(hosts.String()), 0o600),
	)
	data, err := json.Marshal(accounts)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "accounts.json"), data, 0o600))
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST"} {
		t.Setenv(key, "")
	}
	t.Setenv("GH_CONFIG_DIR", filepath.Join(dir, "host-gh"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "no-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

const ghRelayStartCommand = `cp /fixture/gh /usr/bin/gh
cp /fixture/gh /usr/local/bin/gh
cp /fixture/devsy /usr/local/bin/devsy
mkdir -p /root/.local/share/keyrings
export PATH=/usr/local/bin:/usr/bin:/bin
exec dbus-run-session -- sh -c '
 printf "%s" "$DBUS_SESSION_BUS_ADDRESS" > /fixture/bus
 printf fixture-password | gnome-keyring-daemon --unlock >/dev/null 2>/tmp/keyring.log
 /fixture/ghfixture server >/dev/null 2>/tmp/api.log &
 /usr/local/bin/devsy internal agent container ssh-server --address 127.0.0.1:12050 >/tmp/ssh.log 2>&1 &
 /fixture/ghfixture probe
 /usr/sbin/runuser -u vscode -- sh -c "
  echo \$\$ > /home/vscode/gh-relay-server-pid
  exec /usr/local/bin/devsy internal agent container credentials-server --user vscode --configure-github-helper
 "
 sleep 600
'`

func startFixture(
	t ginkgo.GinkgoTInterface,
	ctx context.Context,
	dir string,
	fixture ghRelayFixture,
) (context.CancelFunc, <-chan error) {
	t.Helper()
	name := fixture.name
	image := name + ":test"

	//nolint:gosec // All mounts, commands, and image names belong to this disposable fixture.
	guest := exec.CommandContext(
		ctx,
		"docker",
		"run",
		"--rm",
		"--user",
		"root",
		"--platform",
		"linux/"+fixture.arch,
		"--name",
		name,
		"--network",
		"none",
		"-i",
		"-v",
		dir+":/fixture",
		image,
		"sh",
		"-c",
		ghRelayStartCommand,
	)
	reader, err := guest.StdoutPipe()
	require.NoError(t, err)
	writer, err := guest.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, guest.Start())
	guestDone := make(chan struct{})
	go func() { _ = guest.Wait(); close(guestDone) }()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		//nolint:gosec // name is the random, test-owned container name.
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", name).Run()
		select {
		case <-guestDone:
		case <-cleanup.Done():
			t.Error("fixture container process did not exit during cleanup")
		}
	})
	return startFixtureRelay(t, ctx, reader, writer)
}

func startFixtureRelay(
	t ginkgo.GinkgoTInterface,
	ctx context.Context,
	reader io.ReadCloser,
	writer io.WriteCloser,
) (context.CancelFunc, <-chan error) {
	tunnelCtx, tunnelCancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		done <- tunnelserver.RunServicesServer(tunnelCtx, tunnelserver.ServicesSpec{
			Reader:  reader,
			Writer:  writer,
			Options: []tunnelserver.Option{tunnelserver.WithAllowGitHubCredentials(true)},
		})
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			tunnelCancel()
			_ = writer.Close()
			_ = reader.Close()
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Error("fixture relay did not exit during cleanup")
			}
		})
	}
	// Registered after the environment cleanup, so no host authentication
	// environment is restored while the relay can still resolve accounts.
	t.Cleanup(stop)
	return stop, done
}

func waitFixture(t ginkgo.GinkgoTInterface, ctx context.Context, name string, done <-chan error) {
	t.Helper()
	ready := false
	for range 150 {
		//nolint:gosec // name is the generated test container.
		c := exec.CommandContext(
			ctx,
			"docker",
			"exec",
			name,
			"sh",
			"-c",
			"test -f /fixture/api-ready && test -f /usr/local/bin/gh.devsy-original",
		)
		if c.Run() == nil {
			ready = true
			break
		}
		select {
		case err := <-done:
			t.Fatalf("relay ended before readiness: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	require.True(t, ready, "fixture was not ready")
}

func verifyRelayFallback(
	t ginkgo.GinkgoTInterface,
	ctx context.Context,
	name string,
	stop context.CancelFunc,
) {
	t.Helper()
	// A transport failure must execute normal gh once, without relayed auth.
	stop()
	//nolint:gosec // name is the test-owned container; only its relay process is stopped.
	_ = exec.CommandContext(ctx, "docker", "exec", name, "sh", "-c", "pkill -x devsy || true").
		Run()
	//nolint:gosec // name is the test-owned container, with an isolated configuration.
	fallback := exec.CommandContext(
		ctx,
		"docker",
		"exec",
		name,
		"bash",
		"-lc",
		"export XDG_CACHE_HOME=/fixture/guest-cache; "+
			"GH_CONFIG_DIR=/fixture/guest-gh gh config get editor && "+
			"GH_CONFIG_DIR=/fixture/fallback-gh gh config set editor nano && "+
			"GH_CONFIG_DIR=/fixture/fallback-gh gh config get editor",
	)
	var stderr bytes.Buffer
	fallback.Stderr = &stderr
	plain, err := fallback.Output()
	require.NoError(t, err, "fallback: %s", plain)
	require.Equal(t, "pending\nnano\n", string(plain))
	require.Contains(t, stderr.String(), "gh relay: forwarding unavailable")
	require.Contains(t, stderr.String(), "connection_refused")
}
