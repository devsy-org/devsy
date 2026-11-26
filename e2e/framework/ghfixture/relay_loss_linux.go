//go:build linux

package ghfixture

import (
	"context"
	"encoding/json"
	"io"
	"strings"
)

func runRelayLossChecks(ctx context.Context, root string, accounts Accounts, out io.Writer) int {
	c := newChecks(ctx, root, accounts)
	ok := c.checkRelayLossKeyring()
	c.report.Completed = ok
	if json.NewEncoder(out).Encode(c.report) != nil || !ok {
		return 1
	}
	return 0
}

func (c *checks) checkRelayLossKeyring() bool {
	host := Hosts[1]
	lookup := execute(
		c.ctx,
		processOptions{env: c.env},
		realGH,
		"auth",
		"token",
		"--hostname",
		host,
	)
	if !c.verify(
		"original CLI retrieves same token from keyring after relay stops",
		lookup.code == 0 && strings.TrimSpace(lookup.stdout) == c.accounts[host].Forwarded,
	) {
		return false
	}
	r := execute(
		c.ctx,
		processOptions{env: c.env},
		realGH,
		commandAPI,
		"user",
		"--hostname",
		host,
		"--jq",
		".login",
	)
	if !c.verify(
		"original CLI authenticates after relay stops",
		r.code == 0 && strings.TrimSpace(r.stdout) == hostUser,
	) {
		return false
	}
	r = execute(
		c.ctx,
		processOptions{env: c.env},
		"/usr/local/bin/gh",
		commandAPI,
		"user",
		"--hostname",
		host,
		"--jq",
		".login",
	)
	if !c.verify(
		"wrapper preserves local keyring identity after relay stops",
		r.code == 0 && strings.TrimSpace(r.stdout) == hostUser,
	) {
		return false
	}
	return c.checkRelayLossLogout(host)
}

func (c *checks) checkRelayLossLogout(host string) bool {
	logout := execute(
		c.ctx,
		processOptions{env: c.env},
		realGH,
		"auth",
		"logout",
		"--hostname",
		host,
		"--user",
		hostUser,
	)
	lookup := execute(
		c.ctx,
		processOptions{env: c.env},
		realGH,
		"auth",
		"token",
		"--hostname",
		host,
	)
	if !c.verify(
		"original CLI logout removes keyring credential",
		logout.code == 0 && lookup.code != 0 && lookup.stdout == "",
	) {
		return false
	}
	return c.verify(
		"recovery leaves no forwarded session credentials",
		!c.hasSessions() && c.noForwardedSaved(),
	)
}
