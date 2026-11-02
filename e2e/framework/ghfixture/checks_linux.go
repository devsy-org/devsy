//go:build linux

package ghfixture

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func (c *checks) run() bool {
	for _, phase := range []func() bool{
		c.checkInstallation, c.checkRelayHosts, c.checkGuestRegistrations, c.checkDispatcher, c.checkEnvfile, c.checkDispatch,
		c.checkSelection, c.checkSelfReferences, c.checkPreferences, c.checkAuthentication, c.checkLocalAuthentication,
		c.checkKeyring, c.checkExtensions, c.checkConcurrentUpdates, c.checkWriteback,
		c.checkMalformedWorkingHosts, c.checkOrphanChildren,
		c.checkAuthenticationRaces, c.checkHost, c.checkConfigLocations, c.checkRelayLoss,
	} {
		if !phase() {
			return false
		}
	}
	return true
}

func (c *checks) checkInstallation() bool {
	if !c.verify("credentials server stays non-root", nonRootCredentialsService()) {
		return false
	}
	info, err := os.Stat("/usr/local/bin")
	if !c.verify(
		"wrapper directory remains protected",
		err == nil && info.Mode().Perm()&0o022 == 0,
	) {
		return false
	}
	return c.checkInstalledFiles()
}

func (c *checks) checkInstalledFiles() bool {
	before, _ := readFixture(filepath.Join(c.root, "pre-install-gh"))
	if !c.verify(
		"SSH server predates wrapper installation",
		strings.TrimSpace(string(before)) == "/usr/local/bin/gh",
	) {
		return false
	}
	wrapper, _ := readFixture("/usr/local/bin/gh")
	if !c.verify(
		"standard wrapper installed",
		strings.HasPrefix(string(wrapper), "#!/bin/sh\n# devsy gh relay\n"),
	) {
		return false
	}
	backup, _ := readFixture(realGH)
	release, _ := readFixture(filepath.Join(c.root, "gh"))
	if !c.verify(
		"real gh preserved in fixed backup",
		len(backup) > 0 && bytes.Equal(backup, release),
	) {
		return false
	}
	if !c.verify("no dedicated profile installed", !exists("/etc/profile.d/devsy-gh.sh")) {
		return false
	}
	c.hostBefore, _ = readFixture(filepath.Join(c.root, "host-gh", "hosts.yml"))
	return os.MkdirAll(c.original, 0o700) == nil
}

func (c *checks) checkGuestRegistrations() bool {
	c.registrations = `github.com:
    git_protocol: https
    user: local-user
    users:
        local-user:
enterprise.one.test:
    git_protocol: https
    user: host-user
    oauth_token: ""
    users:
        host-user:
            oauth_token: ""
enterprise.two.test:
    git_protocol: https
    user: dotfiles-user
    oauth_token:
    users:
        dotfiles-user:
            oauth_token:
        other-user:
`

	if !writeFixture(filepath.Join(c.original, "hosts.yml"), c.registrations) {
		return false
	}
	for _, host := range Hosts {
		if !c.verify(
			"registered identity blocks ambiguous keyring: "+host,
			!c.api(host, hostUser, nil),
		) {
			return false
		}
	}
	if !c.verify(
		"unchanged invocation preserves exact dotfiles registrations",
		c.saved() == c.registrations && c.noForwardedSaved(),
	) {
		return false
	}
	return os.Remove(filepath.Join(c.original, "hosts.yml")) == nil
}

func (c *checks) checkDispatcher() bool {
	c.dispatcher = filepath.Join(c.root, "dispatcher")
	if !executableFixture(
		filepath.Join(c.dispatcher, "gh"),
		`#!/bin/sh
export PATH=/usr/bin:/usr/local/bin:/bin
if [ -n "$GH_TEST_CONFIG_OVERRIDE" ]; then
  export GH_CONFIG_DIR="$GH_TEST_CONFIG_OVERRIDE"
fi
exec /usr/local/bin/gh "$@"
`,
	) {
		return false
	}
	for _, host := range Hosts {
		if !c.verify(
			"dispatcher re-entry uses local-only lookup: "+host,
			c.nestedAPI(host, hostUser, nil),
		) {
			return false
		}
	}
	if !c.verify(
		"dispatcher re-entry preserves real auth status",
		c.nested("", nil, "auth", "status").code == 0,
	) {
		return false
	}
	return c.checkDispatcherConfiguration()
}

func (c *checks) checkDispatcherConfiguration() bool {
	redirected := filepath.Join(c.root, "redirected-gh")
	if os.MkdirAll(redirected, 0o700) != nil {
		return false
	}
	if !c.verify(
		"local-only lookup survives dispatcher config changes",
		c.nestedAPI(Hosts[0], hostUser, map[string]string{"GH_TEST_CONFIG_OVERRIDE": redirected}),
	) {
		return false
	}
	if !c.verify(
		"nested lookup leaves original preferences unchanged",
		c.saved() == "" && c.noForwardedSaved(),
	) {
		return false
	}
	if !c.verify(
		"nested lookup retains explicit invalid environment authentication",
		!c.nestedAPI(Hosts[0], hostUser, map[string]string{envToken: c.accounts[Hosts[0]].Invalid}),
	) {
		return false
	}
	return c.checkNestedLocalLogin()
}

func (c *checks) checkNestedLocalLogin() bool {
	r := c.nested(
		c.accounts[Hosts[0]].Local,
		nil,
		"auth",
		"login",
		"--hostname",
		Hosts[0],
		"--with-token",
		"--insecure-storage",
	)
	if !c.verify(
		"nested local login retains precedence",
		r.code == 0 && c.nestedAPI(Hosts[0], localUser, nil),
	) {
		return false
	}
	local := c.saved()
	if !writeFixture(
		filepath.Join(c.original, "hosts.yml"),
		strings.ReplaceAll(local, c.accounts[Hosts[0]].Local, c.accounts[Hosts[0]].Invalid),
	) {
		return false
	}
	if !c.verify(
		"nested invalid local token never falls back",
		!c.nestedAPI(Hosts[0], hostUser, nil),
	) {
		return false
	}
	if !writeFixture(filepath.Join(c.original, "hosts.yml"), local) {
		return false
	}
	return c.checkNestedLogout()
}

func (c *checks) checkNestedLogout() bool {
	r := c.nested("", nil, "auth", "logout", "--hostname", Hosts[0], "--user", localUser)
	if !c.verify(
		"nested local logout restores forwarding",
		r.code == 0 && c.nestedAPI(Hosts[0], hostUser, nil),
	) {
		return false
	}
	r = c.gh(
		"",
		nil,
		"alias",
		"set",
		"probe-guard",
		"!test -z \"$DEVSY_GH_AUTH_PROBE\" && gh api user --jq .login",
	)
	if !c.verify("nested alias preparation", r.code == 0) {
		return false
	}
	r = c.nested("", nil, "probe-guard")
	if !c.verify(
		"local-only marker does not leak into aliases or their children",
		r.code == 0 && strings.TrimSpace(r.stdout) == hostUser,
	) {
		return false
	}
	return c.verify(
		"nested alias removed",
		c.gh("", nil, "alias", "delete", "probe-guard").code == 0,
	)
}

func (c *checks) checkEnvfile() bool {
	path := "/etc/envfile.json"
	before, err := readFixture(path)
	defer restoreFixture(path, before, err)
	b, _ := json.Marshal(
		map[string]any{"env": map[string]string{envToken: c.accounts[Hosts[0]].Invalid}},
	)
	if !writeFixture(path, string(b)) {
		return false
	}
	if !c.verify(
		"Devsy envfile does not replace forwarded authentication",
		c.api(Hosts[0], hostUser, nil),
	) {
		return false
	}
	return c.verify(
		"Devsy envfile does not replace explicit authentication",
		c.api(Hosts[0], localUser, map[string]string{envToken: c.accounts[Hosts[0]].Local}),
	)
}

func restoreFixture(path string, before []byte, readErr error) {
	if os.IsNotExist(readErr) {
		_ = os.Remove(path)
		return
	}
	_ = writeFixture(path, string(before))
}

func (c *checks) checkDispatch() bool {
	for _, args := range [][]string{
		{"--help"},
		{"--version"},
		{commandAPI, "--devsy-unknown-fixture-flag"},
		{commandAPI, "--context", "sample"},
		{commandAPI, "--log-level", "invalid"},
		{commandAPI, "--log-output", "json"},
	} {
		expected := execute(c.ctx, processOptions{env: c.env, input: ""}, realGH, args...)
		actual := c.gh("", nil, args...)
		if !c.verify(
			"real gh arguments pass through Cobra: "+strings.Join(args, " "),
			actual == expected,
		) {
			return false
		}
	}
	return c.checkLogLevels()
}

func (c *checks) checkLogLevels() bool {
	for _, level := range []string{debugLevel, "trace"} {
		r := c.gh("", map[string]string{envLogLevel: level}, commandAPI, "user", "--jq", ".login")
		if !c.verify(
			level+" enables relay logs without changing stdout",
			r.code == 0 && r.stdout == "host-user\n" && strings.Contains(r.stderr, "gh relay: authentication source") &&
				strings.Contains(r.stderr, "forwarded"),
		) {
			return false
		}
	}
	r := c.gh("", nil, commandAPI, "user", "--jq", ".login")
	return c.verify(
		"normal successful forwarding is silent",
		r.code == 0 && r.stdout == "host-user\n" && r.stderr == "",
	)
}

func (c *checks) checkSelection() bool {
	path := filepath.Join(c.original, "hosts.yml")
	before, readErr := readFixture(path)
	if !writeFixture(path, "[invalid yaml") {
		return false
	}
	expected := execute(c.ctx, processOptions{env: c.env, input: ""}, realGH, "--version")
	actual := c.gh("", nil, "--version")
	if !c.verify(
		"overlay failure preserves gh stdout and exit",
		actual.code == expected.code && actual.stdout == expected.stdout,
	) {
		return false
	}
	if !c.verify(
		"overlay failure warns without config content",
		strings.Contains(actual.stderr, "gh relay: forwarding unavailable") &&
			strings.Contains(actual.stderr, "schema_invalid"),
	) {
		return false
	}
	restoreFixture(path, before, readErr)
	return c.checkFixedBackup()
}

func (c *checks) checkFixedBackup() bool {
	r := c.gh("", map[string]string{envLogLevel: debugLevel}, commandAPI, "user", "--jq", ".login")
	if !c.verify(
		"non-PTY SSH uses fixed real-gh backup",
		r.code == 0 && strings.TrimSpace(r.stdout) == hostUser &&
			strings.Contains(r.stderr, realGH),
	) {
		return false
	}
	return c.checkPathSelection()
}

func (c *checks) checkPathSelection() bool {
	selected := filepath.Join(c.root, "selected-gh")
	if os.MkdirAll(selected, 0o700) != nil {
		return false
	}
	if os.Symlink("/usr/bin/gh", filepath.Join(selected, "gh")) != nil {
		return false
	}
	r := execute(
		c.ctx,
		processOptions{
			env: overlayEnvironment(
				c.env,
				map[string]string{
					envPath:     selected + ":/usr/local/bin:/usr/bin:/bin",
					envLogLevel: debugLevel,
				},
			),
			input: "",
		},
		"/usr/local/bin/gh",
		commandAPI,
		"user",
		"--jq",
		".login",
	)
	c.stderr = append(c.stderr, r.stderr)
	if !c.verify(
		"repo-selected gh precedes fixed backup",
		r.code == 0 && strings.TrimSpace(r.stdout) == hostUser &&
			strings.Contains(r.stderr, filepath.Join(selected, "gh")),
	) {
		return false
	}
	return c.checkBackupOutsidePath()
}

func (c *checks) checkBackupOutsidePath() bool {
	isolated := filepath.Join(c.root, "isolated-path")
	if os.MkdirAll(isolated, 0o700) != nil {
		return false
	}
	r := execute(
		c.ctx,
		processOptions{
			env: overlayEnvironment(
				c.env,
				map[string]string{envPath: isolated, envLogLevel: debugLevel},
			),
			input: "",
		},
		"/usr/local/bin/gh",
		commandAPI,
		"user",
		"--jq",
		".login",
	)
	c.stderr = append(c.stderr, r.stderr)
	return c.verify(
		"explicit wrapper uses backup outside PATH",
		r.code == 0 && strings.TrimSpace(r.stdout) == hostUser &&
			strings.Contains(r.stderr, realGH),
	)
}

func (c *checks) checkSelfReferences() bool {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".gitconfig")
	before, err := readFixture(path)
	defer restoreFixture(path, before, err)
	if !c.verify(
		"setup-git accepts forwarded authentication",
		c.gh("", nil, "auth", "setup-git").code == 0,
	) {
		return false
	}
	helper := execute(
		c.ctx,
		processOptions{env: c.env, input: ""},
		"git",
		"config",
		"--global",
		"--get-all",
		"credential.https://github.com.helper",
	)
	if !c.verify(
		"setup-git persists the relay wrapper",
		helper.code == 0 && strings.Contains(helper.stdout, "/usr/local/bin/gh auth git-credential") &&
			!strings.Contains(helper.stdout, "gh.devsy-original"),
	) {
		return false
	}
	credential := execute(
		c.ctx,
		processOptions{
			env:   overlayEnvironment(c.env, map[string]string{"GIT_TERMINAL_PROMPT": "0"}),
			input: "protocol=https\nhost=github.com\n\n",
		},
		"git",
		"credential",
		"fill",
	)
	if !c.verify(
		"persisted Git helper retrieves forwarded authentication",
		credential.code == 0 &&
			strings.Contains(credential.stdout, "password="+c.accounts[Hosts[0]].Forwarded),
	) {
		return false
	}
	return c.checkAliasSelfReferences()
}

func (c *checks) checkAliasSelfReferences() bool {
	if !c.verify(
		"self-path alias saves",
		c.gh("", nil, "alias", "set", "self-path", "!printf \"%s\" \"$GH_PATH\"").code == 0,
	) {
		return false
	}
	for _, tc := range []struct {
		name     string
		env      map[string]string
		expected string
	}{
		{"aliases inherit relay self-path", nil, "/usr/local/bin/gh"},
		{"explicit self-path is preserved", map[string]string{envGHPath: customConfigDirectory}, customConfigDirectory},
		{"explicit empty self-path is preserved", map[string]string{envGHPath: ""}, ""},
	} {
		if !c.verify(tc.name, c.gh("", tc.env, "self-path").stdout == tc.expected) {
			return false
		}
	}
	return c.verify(
		"self-path alias removed",
		c.gh("", nil, "alias", "delete", "self-path").code == 0,
	)
}

func (c *checks) checkPreferences() bool {
	path := filepath.Join(c.original, "hosts.yml")
	if !writeFixture(path, c.registrations) {
		return false
	}
	r := c.gh("", nil, "config", "set", "git_protocol", "https", "--host", Hosts[2])
	if !c.verify(
		"host setting resave preserves registrations and user preference",
		r.code == 0 && strings.Contains(c.saved(), "user: dotfiles-user") &&
			strings.Contains(c.saved(), "other-user:"),
	) {
		return false
	}
	if !c.verify(
		"forwarding preserves saved per-account settings",
		strings.TrimSpace(
			c.gh("", nil, "config", "get", "git_protocol", "--host", Hosts[2]).stdout,
		) == "https",
	) {
		return false
	}
	return c.checkPreferenceLogin()
}

func (c *checks) checkPreferenceLogin() bool {
	path := filepath.Join(c.original, "hosts.yml")
	r := c.gh(
		c.accounts[Hosts[0]].Local,
		nil,
		"auth",
		"login",
		"--hostname",
		Hosts[0],
		"--with-token",
		"--insecure-storage",
	)
	if !c.verify(
		"local login from tokenless preferences persists",
		r.code == 0 && c.api(Hosts[0], localUser, nil) && c.noForwardedSaved(),
	) {
		return false
	}
	r = c.gh("", nil, "auth", "logout", "--hostname", Hosts[0], "--user", localUser)
	if !c.verify(
		"local logout from preferences restores forwarding",
		r.code == 0 && c.api(Hosts[0], hostUser, nil),
	) {
		return false
	}
	if os.Remove(path) != nil {
		return false
	}
	return c.checkMetadataUpdates()
}

func nonRootCredentialsService() bool {
	pid, _ := readFixture("/home/vscode/gh-relay-server-pid")
	status, _ := readFixture(filepath.Join("/proc", strings.TrimSpace(string(pid)), "status"))
	nonroot := false
	for line := range strings.SplitSeq(string(status), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			nonroot = true
			for _, uid := range strings.Fields(line)[1:] {
				if uid == "0" {
					nonroot = false
				}
			}
		}
	}

	return nonroot
}
