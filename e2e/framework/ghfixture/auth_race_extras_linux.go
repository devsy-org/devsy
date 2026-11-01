//go:build linux

package ghfixture

import (
	"strings"
)

func (c *checks) checkIndependentHostRace() bool {
	prefix := "auth race different hosts: "
	base, ok := c.prepareAuthenticationRace("mixed", authRace{first: refreshOperation})
	if !c.verify(prefix+"alias prepared", ok) {
		return false
	}
	p := c.pendingAlias(authRaceAlias)
	defer p.stop()
	if !c.verify(prefix+"mutation reached barrier", c.waitReady()) ||
		!c.verify(
			prefix+"parallel login succeeds",
			c.mutateAuthentication(loginOperation, Hosts[2]).code == 0,
		) {
		return false
	}
	if !writeFixture(c.release, "") ||
		!c.verify(prefix+"pending command preserves exit", p.join()) {
		return false
	}
	base[Hosts[0]] = c.expectedAuthentication(base[Hosts[0]], refreshOperation, Hosts[0])
	base[Hosts[2]] = c.expectedAuthentication(raceHost{}, loginOperation, Hosts[2])
	return c.checkIndependentHostResult(prefix, p, base)
}

func (c *checks) checkIndependentHostResult(
	prefix string,
	p *pendingCommand,
	base map[string]raceHost,
) bool {
	return c.verify(
		prefix+"no authentication conflict",
		!strings.Contains(p.stderr.String(), "authentication_conflict"),
	) &&
		c.verify(
			prefix+"both API identities preserved",
			c.originalIdentity(Hosts[0], hostUser) && c.originalIdentity(Hosts[2], secondUser),
		) &&
		c.verify(
			prefix+"complete authentication metadata preserved",
			c.authenticationMatches(base),
		) &&
		c.verify(prefix+"forwarded credentials absent from artifacts", c.authArtifactsClean())
}

func (c *checks) checkIndependentKeyringRace() bool {
	prefix := "auth race independent keyring: "
	host := Hosts[0]
	base, ok := c.prepareAuthenticationRace("mixed", authRace{first: refreshOperation})
	if !c.verify(prefix+"alias prepared", ok) {
		return false
	}
	p := c.pendingAlias(authRaceAlias)
	defer p.stop()
	if !c.verify(prefix+"mutation reached barrier", c.waitReady()) {
		return false
	}
	login := c.originalCommand(
		c.accounts[host].Second,
		"auth",
		"login",
		"--hostname",
		host,
		"--with-token",
	)
	defer c.originalCommand("", "auth", "logout", "--hostname", host, "--user", secondUser)
	if !c.verify(prefix+"parallel secure login succeeds", login.code == 0) ||
		!writeFixture(c.release, "") ||
		!c.verify(prefix+"pending command preserves exit", p.join()) {
		return false
	}
	expected := base[host]
	expected.User, expected.Token = secondUser, ""
	expected.Users[secondUser] = raceAccount{}
	base[host] = expected
	return c.checkIndependentKeyringResult(prefix, p, base)
}

func (c *checks) checkIndependentKeyringResult(
	prefix string,
	p *pendingCommand,
	base map[string]raceHost,
) bool {
	host := Hosts[0]
	token := c.originalCommand("", "auth", "token", "--hostname", host)
	return c.verify(
		prefix+"conflict reported",
		strings.Contains(p.stderr.String(), "authentication_conflict"),
	) &&
		c.verify(
			prefix+"keyring token preserved",
			token.code == 0 && strings.TrimSpace(token.stdout) == c.accounts[host].Second,
		) &&
		c.verify(prefix+"API identity preserved", c.originalIdentity(host, secondUser)) &&
		c.verify(
			prefix+"complete authentication metadata preserved",
			c.authenticationMatches(base),
		) &&
		c.verify(prefix+"forwarded credentials absent from artifacts", c.authArtifactsClean())
}

func (c *checks) originalCommand(input string, args ...string) processResult {
	result := execute(c.ctx, processOptions{env: c.env, input: input}, realGH, args...)
	c.last = result
	c.stderr = append(c.stderr, result.stderr)
	return result
}

func (c *checks) checkLogoutRecovery() bool {
	prefix := "auth race recovery after logout: "
	base, ok := c.prepareAuthenticationRace("mixed", authRace{first: loginOperation})
	if !c.verify(prefix+"alias prepared", ok) {
		return false
	}
	if !c.verify(
		prefix+"pending preference prepared",
		c.setAuthenticationAlias(loginOperation, "auth-race-pending"),
	) {
		return false
	}
	config, err := readFixture(c.target)
	if err != nil {
		return false
	}
	defer restoreFixture(c.target, config, err)
	if !c.checkFailedLogoutWriteback(prefix) || !writeFixture(c.target, string(config)) {
		return false
	}
	return c.finishLogoutRecovery(prefix, base)
}

func (c *checks) checkFailedLogoutWriteback(prefix string) bool {
	p := c.pendingAlias(authRaceAlias)
	defer p.stop()
	if !c.verify(prefix+"mutation reached barrier", c.waitReady()) ||
		!writeFixture(c.target, "editor: [\n") ||
		!writeFixture(c.release, "") ||
		!c.verify(prefix+"failed writeback preserves exit", p.join()) {
		return false
	}
	return c.verify(
		prefix+"failed writeback remains sanitized",
		c.hasSessions() && c.authArtifactsClean(),
	)
}

func (c *checks) finishLogoutRecovery(prefix string, base map[string]raceHost) bool {
	host := Hosts[0]
	if !c.verify(
		prefix+"later logout succeeds",
		c.originalCommand("", "auth", "logout", "--hostname", host, "--user", secondUser).code == 0 &&
			c.originalCommand("", "auth", "logout", "--hostname", host, "--user", hostUser).code == 0,
	) {
		return false
	}
	base[host] = raceHost{}
	if !c.verify(prefix+"API unauthenticated before recovery", c.originalIdentity(host, "")) {
		return false
	}
	return c.verify(
		prefix+"recovery completes",
		c.setting("editor") == "auth-race-pending" && !c.hasSessions(),
	) &&
		c.verify(
			prefix+"API remains unauthenticated after recovery",
			c.originalIdentity(host, ""),
		) &&
		c.verify(prefix+"logout metadata never resurrected", c.authenticationMatches(base)) &&
		c.verify(prefix+"forwarded credentials absent from artifacts", c.authArtifactsClean())
}
