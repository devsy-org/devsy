//go:build linux

package ghfixture

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (c *checks) checkConcurrentUpdates() bool {
	config := filepath.Join(c.original, "config.yml")
	c.target = filepath.Join(c.root, "dotfiles-config.yml")
	if os.Rename(config, c.target) != nil || os.Symlink(c.target, config) != nil {
		return false
	}
	c.ready = filepath.Join(c.root, "command-ready")
	c.release = filepath.Join(c.root, "command-release")
	r := c.gh(
		"",
		nil,
		"alias",
		"set",
		"pending-editor",
		"!gh config set editor pending; touch \"$GH_TEST_READY\"; while test ! -f \"$GH_TEST_RELEASE\"; do sleep 0.05; done",
	)
	if !c.verify("concurrent alias saves", r.code == 0) {
		return false
	}
	p := c.pending()
	defer p.stop()
	if !c.verify("concurrent invocation reached user mutation", c.waitReady()) {
		return false
	}
	return c.checkParallelConfigMutation(p)
}

func (c *checks) checkParallelConfigMutation(p *pendingCommand) bool {
	alias := filepath.Join(c.root, "config-directory-alias")
	if !c.verify("configuration directory alias prepared", os.Symlink(c.original, alias) == nil) {
		return false
	}
	defer func() { _ = os.Remove(alias) }()
	if !c.verify(
		"parallel gh saves unrelated pager",
		c.gh("", map[string]string{envGHConfig: alias}, "config", "set", "pager", "less").code == 0,
	) {
		return false
	}
	b, _ := readFixture(c.target)
	if !writeFixture(
		c.target,
		strings.ReplaceAll(string(b), "editor: nano", "editor: emacs")+"\nprompt: disabled\n",
	) {
		return false
	}
	if !writeFixture(c.release, "") {
		return false
	}
	completed := p.join()
	if !c.verify("directory aliases observe both concurrent preferences", completed &&
		c.setting("editor") == pendingEditor &&
		strings.TrimSpace(
			c.gh("", map[string]string{envGHConfig: alias}, "config", "get", "pager").stdout,
		) == "less") {
		return false
	}
	return c.checkParallelConfigResult(completed)
}

func (c *checks) checkParallelConfigResult(completed bool) bool {
	config := filepath.Join(c.original, "config.yml")
	info, err := os.Lstat(config)
	if !c.verify(
		"parallel and dotfiles changes survive",
		completed &&
			c.setting("editor") == pendingEditor &&
			c.setting("pager") == "less" &&
			c.setting("prompt") == "disabled" && err == nil &&
			info.Mode()&os.ModeSymlink != 0,
	) {
		return false
	}
	return c.checkInterruptedMutation()
}

func (c *checks) checkInterruptedMutation() bool {
	_ = os.Remove(c.ready)
	_ = os.Remove(c.release)
	if !c.verify(
		"prepare interrupted mutation",
		c.gh("", nil, "config", "set", "editor", "vi").code == 0,
	) {
		return false
	}
	p := c.pending()
	defer p.stop()
	if !c.verify("interrupted invocation reached mutation", c.waitReady()) {
		return false
	}
	p.kill(true)
	p.join()
	if !c.verify(
		"next invocation recovers killed wrapper mutation",
		c.setting("editor") == pendingEditor,
	) {
		return false
	}
	return c.verify("recovered session contains no forwarded credentials", c.noForwardedSaved())
}

func (c *checks) setting(key string) string {
	return strings.TrimSpace(c.gh("", nil, "config", "get", key).stdout)
}

func (c *checks) checkWriteback() bool {
	_ = os.Remove(c.ready)
	if !c.verify(
		"prepare pending writeback",
		c.gh("", nil, "config", "set", "editor", "vi").code == 0,
	) {
		return false
	}
	p := c.pending()
	defer p.stop()
	if !c.verify("writeback failure reached user mutation", c.waitReady()) {
		return false
	}
	latest, err := readFixture(c.target)
	if err != nil {
		return false
	}
	return c.checkCorruptWriteback(p, latest)
}

func (c *checks) checkCorruptWriteback(p *pendingCommand, latest []byte) bool {
	if !writeFixture(c.target, "[broken yaml") || !writeFixture(c.release, "") {
		return false
	}
	completed := p.join()
	stderr := p.stderr.String()
	c.stderr = append(c.stderr, stderr)
	restored := writeFixture(c.target, string(latest))
	if !c.verify("writeback failure does not change real gh exit", completed) {
		return false
	}
	if !c.verify(
		"writeback failure emits actionable error",
		strings.Contains(stderr, "configuration writeback pending") &&
			strings.Contains(stderr, "schema_invalid"),
	) {
		return false
	}
	if !restored {
		return false
	}
	return c.checkRecoveredWriteback()
}

func (c *checks) checkRecoveredWriteback() bool {
	r := c.gh("", map[string]string{envLogLevel: debugLevel}, "config", "get", "editor")
	return c.verify(
		"pending writeback recovered after original repaired",
		r.code == 0 && strings.TrimSpace(r.stdout) == pendingEditor &&
			strings.Contains(r.stderr, "configuration recovered"),
	)
}

func (c *checks) checkOrphanChildren() bool {
	_ = os.Remove(c.ready)
	_ = os.Remove(c.release)
	if !c.verify(
		"prepare live-child ownership",
		c.gh("", nil, "config", "set", "editor", "vi").code == 0,
	) {
		return false
	}
	p := c.pending()
	defer p.stop()
	if !c.verify("live child reached mutation", c.waitReady()) {
		return false
	}
	p.kill(false)
	p.orphan = true
	if !c.verify(
		"live child prevents premature recovery",
		c.setting("editor") == "vi" && c.hasSessions(),
	) {
		return false
	}
	if !writeFixture(c.release, "") {
		return false
	}
	p.join()
	return c.checkCompletedOrphan(p)
}

func (c *checks) checkCompletedOrphan(p *pendingCommand) bool {
	ctxDone := time.After(5 * time.Second)
	for {
		if c.setting("editor") == pendingEditor {
			p.orphan = false
			return c.verify("completed orphan child changes recover", true)
		}
		select {
		case <-ctxDone:
			p.kill(true)
			return c.verify("completed orphan child changes recover", false)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (c *checks) hasSessions() bool {
	entries, _ := filepath.Glob(
		filepath.Join(c.root, "guest-cache", "devsy", "gh-relay", "session-*"),
	)
	return len(entries) > 0
}

func (c *checks) checkHost() bool {
	path := filepath.Join(c.root, "host-gh", "hosts.yml")
	current, _ := readFixture(path)
	if !c.verify(
		"host auth unchanged by guest operations",
		bytes.Equal(current, c.hostBefore) &&
			!exists(filepath.Join(c.root, "host-gh", "config.yml")),
	) {
		return false
	}
	return c.checkEmptyHostAccounts(path)
}

func (c *checks) checkEmptyHostAccounts(path string) bool {
	if !writeFixture(path, "{}\n") {
		return false
	}
	r := c.gh("", nil, "config", "get", "editor")
	if !c.verify(
		"no host accounts fall back without warning",
		r.code == 0 && !strings.Contains(r.stderr, "gh relay:"),
	) {
		return false
	}
	r = c.gh("", map[string]string{envLogLevel: debugLevel}, "config", "get", "editor")
	if !c.verify(
		"no host accounts explained at debug level",
		r.code == 0 && strings.Contains(r.stderr, "no_active_accounts"),
	) {
		return false
	}
	return c.checkBrokenHostAccounts(path)
}

func (c *checks) checkBrokenHostAccounts(path string) bool {
	if !writeFixture(path, "[broken yaml") {
		return false
	}
	r := c.gh("", nil, "config", "get", "editor")
	if !c.verify(
		"host authentication failure warns without changing gh exit",
		r.code == 0 && strings.Contains(r.stderr, "forwarding unavailable") &&
			strings.Contains(r.stderr, "config_decode_failed"),
	) {
		return false
	}
	return c.checkSwitchedHostAccount(path)
}

func (c *checks) checkSwitchedHostAccount(path string) bool {
	text := strings.ReplaceAll(string(c.hostBefore), hostUser, secondUser)
	for _, record := range c.accounts {
		text = strings.ReplaceAll(text, record.Forwarded, record.Second)
	}
	if !writeFixture(path, text) {
		return false
	}
	if !c.verify("host active account followed", c.api(Hosts[2], secondUser, nil)) {
		return false
	}
	r := execute(
		c.ctx,
		processOptions{
			env: overlayEnvironment(
				c.env,
				map[string]string{envSessionBus: "unix:path=/nonexistent"},
			),
			input: "",
		},
		"runuser",
		"-u",
		"vscode",
		"--",
		"env",
		"HOME=/home/vscode",
		"GH_CONFIG_DIR=",
		"XDG_CONFIG_HOME=",
		"XDG_CACHE_HOME=/home/vscode/.cache",
		"/bin/bash",
		"-lc",
		"exec gh api user --hostname enterprise.two.test --jq .login",
	)
	return c.verify(
		"non-root target user inherits wrapper and uses own HOME",
		r.code == 0 && strings.TrimSpace(r.stdout) == secondUser,
	)
}

func (c *checks) checkConfigLocations() bool {
	for _, tc := range []struct {
		name string
		env  map[string]string
		path string
	}{
		{"local config dir override", map[string]string{envGHConfig: customConfigDirectory}, "custom-gh/config.yml"},
		{
			"XDG config location",
			map[string]string{envGHConfig: "", envXDGConfig: "/fixture/custom-xdg"},
			"custom-xdg/gh/config.yml",
		},
		{
			"HOME config location",
			map[string]string{envGHConfig: "", envXDGConfig: "", "HOME": "/fixture/custom-home"},
			"custom-home/.config/gh/config.yml",
		},
	} {
		if !c.verify(
			tc.name,
			c.gh("", tc.env, "config", "set", "editor", "emacs").code == 0 &&
				exists(filepath.Join(c.root, tc.path)),
		) {
			return false
		}
	}
	if !c.verify(
		"no diagnostic files created",
		!exists(filepath.Join(c.root, "guest-cache", "devsy", "gh-relay", "diagnostics")),
	) {
		return false
	}
	return c.checkDiagnosticPrivacy()
}

func (c *checks) checkDiagnosticPrivacy() bool {
	for _, stderr := range c.stderr {
		for _, record := range c.accounts {
			for _, token := range []string{record.Forwarded, record.Local, record.Second, record.Invalid, record.Refresh} {
				if strings.Contains(stderr, token) {
					return c.verify("stderr logs contain no credential values", false)
				}
			}
		}
	}
	if !c.verify("stderr logs contain no credential values", true) {
		return false
	}
	return c.verify("normal sessions cleaned", !c.hasSessions())
}

func (c *checks) checkRelayLoss() bool {
	if !c.prepareIndependentKeyring() {
		return false
	}
	_ = os.Remove(c.ready)
	_ = os.Remove(c.release)
	if !c.verify(
		"prepare relay-loss recovery",
		c.gh("", nil, "config", "set", "editor", "vi").code == 0,
	) {
		return false
	}
	p := c.pending()
	defer p.stop()
	ready := c.waitReady()
	p.kill(true)
	p.join()
	return c.verify("relay-loss mutation reached", ready)
}

func (c *checks) prepareIndependentKeyring() bool {
	host := Hosts[1]
	r := c.gh(c.accounts[host].Forwarded, nil, "auth", "login", "--hostname", host, "--with-token")
	return c.verify(
		"prepare independent keyring for relay loss",
		r.code == 0 && c.noForwardedSaved(),
	)
}
