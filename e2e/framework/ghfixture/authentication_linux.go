//go:build linux

package ghfixture

import (
	"os"
	"path/filepath"
	"strings"
)

func (c *checks) checkAuthentication() bool {
	for _, host := range Hosts {
		if !c.verify(
			"all hosts forwarded through real RPC and HTTP relay: "+host,
			c.api(host, hostUser, nil),
		) {
			return false
		}
	}
	if !c.verify("real auth status", c.gh("", nil, "auth", "status").code == 0) {
		return false
	}
	if !c.verify("forwarding never saved", c.noForwardedSaved()) {
		return false
	}
	return c.checkEnvironmentAuthentication()
}

func (c *checks) checkEnvironmentAuthentication() bool {
	r := c.gh("", map[string]string{"GH_HOST": Hosts[2]}, commandAPI, "user", "--jq", ".login")
	if !c.verify("gh chooses GH_HOST", r.code == 0 && strings.TrimSpace(r.stdout) == hostUser) {
		return false
	}
	r = c.gh(
		"",
		map[string]string{envToken: c.accounts[Hosts[0]].Invalid, envLogLevel: debugLevel},
		commandAPI,
		"user",
	)
	if !c.verify(
		"explicit invalid GH_TOKEN never falls back",
		r.code != 0 && strings.Contains(r.stderr, "authentication source") &&
			strings.Contains(r.stderr, "environment"),
	) {
		return false
	}
	if !c.verify(
		"explicit invalid GITHUB_TOKEN never falls back",
		!c.api(Hosts[0], hostUser, map[string]string{"GITHUB_TOKEN": c.accounts[Hosts[0]].Invalid}),
	) {
		return false
	}
	if !c.verify(
		"explicit enterprise environment never falls back",
		!c.api(
			Hosts[1],
			hostUser,
			map[string]string{envEnterpriseToken: c.accounts[Hosts[1]].Invalid},
		),
	) {
		return false
	}
	return c.checkAuthority()
}

func (c *checks) checkAuthority() bool {
	for _, host := range Hosts[1:] {
		patch := map[string]string{envEnterpriseToken: c.accounts[Hosts[0]].Forwarded}
		if !c.verify("wrong-host token rejected REST: "+host, !c.api(host, hostUser, patch)) {
			return false
		}
		r := c.gh(
			"",
			patch,
			commandAPI,
			"graphql",
			"--hostname",
			host,
			"-f",
			"query={viewer{login}}",
		)
		if !c.verify("wrong-host token rejected GraphQL: "+host, r.code != 0) {
			return false
		}
		r = c.gh(
			"",
			nil,
			commandAPI,
			"graphql",
			"--hostname",
			host,
			"-f",
			"query={viewer{login}}",
			"--jq",
			".data.viewer.login",
		)
		if !c.verify(
			"correct-host GraphQL identity: "+host,
			r.code == 0 && strings.TrimSpace(r.stdout) == hostUser,
		) {
			return false
		}
	}
	return true
}

func (c *checks) checkLocalAuthentication() bool {
	host := Hosts[0]
	r := c.gh(
		c.accounts[host].Forwarded,
		nil,
		"auth",
		"login",
		"--hostname",
		host,
		"--with-token",
		"--insecure-storage",
	)
	if !c.verify(
		"same token login stays forwarded",
		r.code == 0 && c.noForwardedSaved() && c.api(host, hostUser, nil),
	) {
		return false
	}
	if !c.verify(
		"auth switch uses forwarded account",
		c.gh("", nil, "auth", "switch", "--hostname", host, "--user", hostUser).code == 0 &&
			c.noForwardedSaved(),
	) {
		return false
	}
	return c.checkRefreshedAuthentication()
}

func (c *checks) checkRefreshedAuthentication() bool {
	host := Hosts[0]
	r := c.gh(
		"",
		map[string]string{envGHBrowser: "/bin/true"},
		"auth",
		"refresh",
		"--hostname",
		host,
		"--insecure-storage",
	)
	if !c.verify(
		"refresh saves a newly issued credential",
		r.code == 0 && strings.Contains(c.saved(), c.accounts[host].Refresh) &&
			c.api(host, hostUser, nil),
	) {
		return false
	}
	if !c.verify(
		"logout refreshed credential",
		c.gh("", nil, "auth", "logout", "--hostname", host, "--user", hostUser).code == 0 &&
			c.api(host, hostUser, nil),
	) {
		return false
	}
	return c.checkFileLogin()
}

func (c *checks) checkFileLogin() bool {
	host := Hosts[0]
	r := c.gh(
		c.accounts[host].Local,
		nil,
		"auth",
		"login",
		"--hostname",
		host,
		"--with-token",
		"--insecure-storage",
	)
	if !c.verify(
		"different token local login persists",
		r.code == 0 && strings.Contains(c.saved(), c.accounts[host].Local) &&
			c.api(host, localUser, nil),
	) {
		return false
	}
	return c.checkInvalidLocalCredential()
}

func (c *checks) checkInvalidLocalCredential() bool {
	r := c.gh("", map[string]string{envLogLevel: debugLevel}, commandAPI, "user", "--jq", ".login")
	if !c.verify(
		"local authentication preference explained at debug level",
		r.code == 0 && r.stdout == "local-user\n" && strings.Contains(r.stderr, "local_config"),
	) {
		return false
	}
	return c.checkInvalidLocalFile()
}

func (c *checks) checkInvalidLocalFile() bool {
	host := Hosts[0]
	local := c.saved()
	if !writeFixture(
		filepath.Join(c.original, "hosts.yml"),
		strings.ReplaceAll(local, c.accounts[host].Local, c.accounts[host].Invalid),
	) {
		return false
	}
	if !c.verify("invalid local credential never falls back", !c.api(host, hostUser, nil)) {
		return false
	}
	if !writeFixture(filepath.Join(c.original, "hosts.yml"), local) {
		return false
	}
	r := c.gh("", nil, "auth", "logout", "--hostname", host, "--user", localUser)
	return c.verify(
		"logout restores forwarding on next invocation",
		r.code == 0 && c.api(host, hostUser, nil) && c.noForwardedSaved(),
	)
}

func (c *checks) checkKeyring() bool {
	host := Hosts[1]
	r := c.gh(c.accounts[host].Local, nil, "auth", "login", "--hostname", host, "--with-token")
	if !c.verify(
		"keyring login succeeds without plaintext token",
		r.code == 0 && !strings.Contains(c.saved(), c.accounts[host].Local) &&
			c.api(host, localUser, nil),
	) {
		return false
	}
	r = c.gh(
		"",
		map[string]string{envLogLevel: debugLevel},
		commandAPI,
		"user",
		"--hostname",
		host,
		"--jq",
		".login",
	)
	if !c.verify(
		"existing keyring credential excluded from forwarding",
		r.code == 0 && strings.TrimSpace(r.stdout) == localUser &&
			strings.Contains(r.stderr, "keyring"),
	) {
		return false
	}
	return c.checkKeyringAvailability()
}

func (c *checks) checkKeyringAvailability() bool {
	host := Hosts[1]
	if !c.verify(
		"nested lookup preserves keyring authentication",
		c.nestedAPI(host, localUser, nil),
	) {
		return false
	}
	if !c.verify(
		"unavailable keyring preserves registered guest identity",
		!c.api(host, hostUser, map[string]string{envSessionBus: "unix:path=/nonexistent"}),
	) {
		return false
	}
	if !c.verify("unavailable backend leaves local keyring intact", c.api(host, localUser, nil)) {
		return false
	}
	return c.checkKeyringStatus()
}

func (c *checks) checkKeyringStatus() bool {
	host := Hosts[1]
	if !c.checkRejectedKeyring() {
		return false
	}
	r := c.gh("", nil, "auth", "status", "--hostname", host)
	if !c.verify(
		"real auth status identifies keyring",
		r.code == 0 && strings.Contains(r.stdout+r.stderr, "keyring"),
	) {
		return false
	}
	if !c.verify(
		"keyring logout restores forwarding",
		c.gh("", nil, "auth", "logout", "--hostname", host, "--user", localUser).code == 0 &&
			c.api(host, hostUser, nil),
	) {
		return false
	}
	return c.checkSameTokenKeyring()
}

func (c *checks) checkSameTokenKeyring() bool {
	host := Hosts[1]
	r := c.gh(c.accounts[host].Forwarded, nil, "auth", "login", "--hostname", host, "--with-token")
	if !c.verify(
		"same token explicit keyring login retained",
		r.code == 0 && strings.Contains(c.saved(), hostUser) && c.noForwardedSaved() &&
			c.api(host, hostUser, nil),
	) {
		return false
	}
	return c.checkSameTokenKeyringProof()
}

func (c *checks) checkSameTokenKeyringProof() bool {
	host := Hosts[1]
	lookup := execute(
		c.ctx,
		processOptions{env: c.env, input: ""},
		realGH,
		"auth",
		"token",
		"--hostname",
		host,
	)
	if !c.verify(
		"same token keyring verified through original real CLI",
		lookup.code == 0 && strings.TrimSpace(lookup.stdout) == c.accounts[host].Forwarded,
	) {
		return false
	}
	return c.verify(
		"same token keyring logout restores forwarding",
		c.gh("", nil, "auth", "logout", "--hostname", host, "--user", hostUser).code == 0 &&
			c.api(host, hostUser, nil),
	)
}

func (c *checks) checkRejectedKeyring() bool {
	host := Hosts[1]
	revoked := filepath.Join(c.root, "revoke-local")
	if !writeFixture(revoked, "") {
		return false
	}
	valid := c.verify("expired keyring credential never falls back", !c.api(host, hostUser, nil)) &&
		c.verify(
			"nested expired keyring credential never falls back",
			!c.nestedAPI(host, hostUser, nil),
		)
	_ = os.Remove(revoked)
	if !valid {
		return false
	}
	forbidden := filepath.Join(c.root, "forbid-local")
	if !writeFixture(forbidden, "") {
		return false
	}
	defer func() { _ = os.Remove(forbidden) }()
	r := c.gh("", nil, commandAPI, "repos/fixture/private", "--hostname", host)
	if !c.verify(
		"insufficient keyring permission never falls back",
		r.code != 0 && strings.Contains(r.stderr, "403"),
	) {
		return false
	}
	r = c.nested("", nil, commandAPI, "repos/fixture/private", "--hostname", host)
	return c.verify(
		"nested insufficient keyring permission never falls back",
		r.code != 0 && strings.Contains(r.stderr, "403"),
	)
}

func (c *checks) checkExtensions() bool {
	if !c.verify(
		"alias saves",
		c.gh("", nil, "alias", "set", "save-editor", "!gh config set editor nano").code == 0,
	) {
		return false
	}
	if !c.verify(
		"alias nested configuration persists",
		c.gh("", nil, "save-editor").code == 0 &&
			strings.TrimSpace(c.gh("", nil, "config", "get", "editor").stdout) == "nano",
	) {
		return false
	}
	ext := filepath.Join(c.root, "guest-data", "gh", "extensions", "gh-probe", "gh-probe")
	if !executableFixture(
		ext,
		`#!/bin/sh
gh config set pager cat
gh api user --hostname enterprise.two.test --jq .login
printf 'extension-out\n'
printf 'extension-err\n' >&2
exit 7
`,
	) {
		return false
	}
	r := c.gh("", nil, "probe")
	if !c.verify(
		"extension streams and failure exit pass unchanged",
		r.code == 7 && r.stdout == "host-user\nextension-out\n" && r.stderr == "extension-err\n",
	) {
		return false
	}
	return c.verify(
		"extension change persists after failure",
		strings.TrimSpace(c.gh("", nil, "config", "get", "pager").stdout) == "cat",
	)
}
