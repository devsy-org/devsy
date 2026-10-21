package ssh

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
)

const publicGHAPICommand = "GH_CONFIG_DIR=/tmp/public-gh HTTPS_PROXY=http://127.0.0.1:8123 " +
	"NO_PROXY= SSL_CERT_FILE=/fixture/cert.pem gh api user --jq .login"

const publicGHUser = "host-user"

const flagNoServices = "--start-services=false"

// This exercises the public command with no credential-listener or wrapper
// polling. Only the independent, dummy GitHub API starts during workspace setup.
func testPublicGitHubCLIForwarding(ctx context.Context) {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	prepareHostAuthentication(t, dir)
	fixture := prepareFixture(t, ctx, dir)
	t.Setenv("DEVSY_HOME", publicTestHome(t))
	t.Setenv("DEVSY_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	bin, err := filepath.Abs("bin")
	require.NoError(t, err)
	f := framework.NewDefaultFramework(bin)
	require.NoError(t, f.DevsyProviderAdd(ctx, "docker"))
	require.NoError(t, f.DevsyProviderUse(ctx, "docker"))
	setPublicForwarding(t, ctx, f, false)
	for _, user := range []string{"root", "vscode"} {
		t.Logf("public SSH guest user: %s", user)
		(&publicGHFixture{f: f, dir: dir, fixture: fixture}).testUser(t, ctx, user)
	}
}

func setPublicForwarding(
	t ginkgo.GinkgoTInterface,
	ctx context.Context,
	f *framework.Framework,
	enabled bool,
) {
	t.Helper()
	value := "false"
	if enabled {
		value = "true"
	}
	_, _, err := f.ExecCommandCapture(ctx, []string{
		"context", "set", "default", "-o", "SSH_INJECT_GITHUB_CREDENTIALS=" + value,
		"-o", "SSH_AGENT_FORWARDING=false",
		"-o", "SSH_INJECT_GIT_CREDENTIALS=false", "-o", "SSH_INJECT_DOCKER_CREDENTIALS=false",
	})
	require.NoError(t, err)
}

type publicGHFixture struct {
	f       *framework.Framework
	dir     string
	fixture ghRelayFixture
}

func (p *publicGHFixture) testUser(t ginkgo.GinkgoTInterface, ctx context.Context, user string) {
	t.Helper()
	workspace := t.TempDir()
	apiDir := p.apiDirectory(t)
	configuration := map[string]any{
		"image":      p.fixture.name + ":test",
		"remoteUser": user,
		"mounts":     []string{"type=bind,source=" + apiDir + ",target=/fixture"},
		"postCreateCommand": `sudo -n sh -c 'nohup /opt/ghfixture server-public >/tmp/fixture-api.log 2>&1 </dev/null &'
for i in $(seq 1 50); do test -f /fixture/api-ready && exit 0; sleep .1; done
exit 1`,
	}
	data, err := json.Marshal(configuration)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), data, 0o600))
	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = p.f.DevsyWorkspaceDelete(cleanup, workspace)
	})
	require.NoError(t, p.f.DevsyUp(ctx, workspace))
	p.registerDiagnostics(t, workspace, &deleted)
	// No services start during this check, leaving the first enabled SSH call
	// responsible for both preparing the relay and running the first gh command.
	_, _, err = p.f.ExecCommandCapture(ctx, []string{
		cmdWorkspace, cmdSSH, workspace, flagNoServices, flagCommand,
		"test ! -e /usr/local/bin/gh.devsy-original",
	})
	require.NoError(t, err)
	setPublicForwarding(t, ctx, p.f, true)
	stop := p.testLiveServiceReuse(t, ctx, workspace)
	setPublicForwarding(t, ctx, p.f, false)
	out, err := p.f.DevsySSHOnce(
		ctx,
		workspace,
		"if "+publicGHAPICommand+" >/dev/null 2>&1; then exit 1; fi; gh config set editor nano && gh config get editor",
	)
	require.NoError(t, err)
	require.Equal(t, "nano", strings.TrimSpace(out))
	stop()
	p.testMissingCLI(t, ctx, workspace)
	require.NoError(t, p.f.DevsyWorkspaceDelete(ctx, workspace))
	deleted = true
}

func (p *publicGHFixture) apiDirectory(t ginkgo.GinkgoTInterface) string {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	accounts, err := os.ReadFile(filepath.Join(p.dir, "accounts.json"))
	require.NoError(t, err)
	path := filepath.Join(dir, "accounts.json")
	require.NoError(t, root.WriteFile("accounts.json", accounts, 0o600))
	// #nosec G302 -- Only generated fixture resources are shared with the guest UID.
	require.NoError(t, os.Chmod(dir, 0o755))
	// #nosec G302 -- The generated dummy API accounts must be readable by the guest UID.
	require.NoError(t, os.Chmod(path, 0o644))
	return dir
}

func (p *publicGHFixture) testMissingCLI(
	t ginkgo.GinkgoTInterface,
	ctx context.Context,
	workspace string,
) {
	t.Helper()
	_, _, err := p.f.ExecCommandCapture(ctx, []string{
		cmdWorkspace, cmdSSH, workspace,
		flagNoServices, flagCommand,
		"sudo -n rm -f /usr/local/bin/gh /usr/local/bin/gh.devsy-original /usr/bin/gh",
	})
	require.NoError(t, err)
	setPublicForwarding(t, ctx, p.f, true)
	_, err = p.f.DevsySSHOnce(ctx, workspace, "test ! -e /usr/local/bin/gh && ! command -v gh")
	require.NoError(t, err, "missing backend must not manufacture a gh command")
	_, err = p.f.DevsySSHOnce(
		ctx,
		workspace,
		"sudo -n install -m 755 /opt/fixture-gh /usr/bin/gh && sudo -n install -m 755 /opt/fixture-gh /usr/local/bin/gh",
	)
	require.NoError(t, err)
	out, err := p.f.DevsySSHOnce(ctx, workspace, publicGHAPICommand)
	require.NoError(t, err, "a later connection must retry installation after gh becomes available")
	require.Equal(t, publicGHUser, strings.TrimSpace(out))
	setPublicForwarding(t, ctx, p.f, false)
}

func publicTestHome(t ginkgo.GinkgoTInterface) string {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "gh-public-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	return dir
}
