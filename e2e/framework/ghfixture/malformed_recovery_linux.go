//go:build linux

package ghfixture

import (
	"path/filepath"
	"strings"
)

func (c *checks) checkMalformedWorkingHosts() bool {
	path := filepath.Join(c.original, "hosts.yml")
	before, err := readFixture(path)
	defer restoreFixture(path, before, err)
	alias := `!umask 077
file="$GH_CONFIG_DIR/hosts.yml"
scratch="$GH_CONFIG_DIR/malformed-hosts"
printf '%s\n' 'invalid.test:' '  users: [alice, bob]' > "$scratch" &&
cat "$file" >> "$scratch" && mv "$scratch" "$file"`
	if !c.verify(
		"malformed working hosts: alias prepared",
		c.gh("", nil, "alias", "set", "malformed-hosts", alias).code == 0,
	) {
		return false
	}
	r := c.gh("", nil, "malformed-hosts")
	if !c.verify("malformed working hosts: child exit preserved", r.code == 0) ||
		!c.verify(
			"malformed working hosts: lost intent reported",
			strings.Contains(r.stderr, "configuration writeback pending") &&
				strings.Contains(r.stderr, "session.lost_intent"),
		) ||
		!c.verify(
			"malformed working hosts: original authentication preserved",
			c.saved() == string(before),
		) ||
		!c.verify(
			"malformed working hosts: forwarded credentials scrubbed",
			c.authArtifactsClean(),
		) {
		return false
	}
	return c.verify(
		"malformed working hosts: next invocation recovers",
		c.gh("", nil, "alias", "delete", "malformed-hosts").code == 0 && !c.hasSessions(),
	)
}

func (c *checks) checkMalformedLatestHostsRecovery() bool {
	path := filepath.Join(c.original, "hosts.yml")
	before, err := readFixture(path)
	defer restoreFixture(path, before, err)
	base, ok := c.prepareAuthenticationRace("mixed", authRace{first: loginOperation})
	if !c.verify("malformed latest hosts: alias prepared", ok) {
		return false
	}
	baseline, err := readFixture(path)
	if err != nil {
		return false
	}
	if !c.failMalformedLatestWriteback(path) {
		return false
	}
	return c.finishMalformedLatestRecovery(path, baseline, base)
}

func (c *checks) failMalformedLatestWriteback(path string) bool {
	p := c.pendingAlias(authRaceAlias)
	defer p.stop()
	if !c.verify("malformed latest hosts: mutation reached barrier", c.waitReady()) {
		return false
	}
	if !writeFixture(path, malformedLatestHosts) || !writeFixture(c.release, "") ||
		!c.verify("malformed latest hosts: child exit preserved", p.join()) {
		return false
	}
	c.stderr = append(c.stderr, p.stderr.String())
	return c.verify(
		"malformed latest hosts: pending writeback sanitized",
		c.hasSessions() && c.authArtifactsClean(),
	)
}

func (c *checks) finishMalformedLatestRecovery(
	path string,
	baseline []byte,
	base map[string]raceHost,
) bool {
	patch := map[string]string{envGHConfig: filepath.Join(c.root, "recovery-probe-gh")}
	if !c.checkRepeatedMalformedRecovery(patch) || !writeFixture(path, string(baseline)) {
		return false
	}
	r := c.gh("", patch, "--version")
	if !c.verify(
		"malformed latest hosts: repaired original recovers",
		r.code == 0 && !c.hasSessions(),
	) {
		return false
	}
	base[Hosts[0]] = c.expectedAuthentication(base[Hosts[0]], loginOperation, Hosts[0])
	return c.verify(
		"malformed latest hosts: deliberate local login preserved",
		c.originalIdentity(Hosts[0], secondUser) && c.authenticationMatches(base),
	) &&
		c.verify(
			"malformed latest hosts: forwarded credentials absent from artifacts",
			c.authArtifactsClean(),
		)
}

func (c *checks) checkRepeatedMalformedRecovery(patch map[string]string) bool {
	for range 2 {
		if c.gh("", patch, "--version").code != 0 || c.saved() != malformedLatestHosts ||
			!c.hasSessions() ||
			!c.authArtifactsClean() {
			return c.verify(
				"malformed latest hosts: repeated recovery preserves invalid original and CLI exit",
				false,
			)
		}
	}
	return c.verify(
		"malformed latest hosts: repeated recovery preserves invalid original and CLI exit",
		true,
	)
}

const malformedLatestHosts = "github.com:\n  users: [alice, bob]\n"
