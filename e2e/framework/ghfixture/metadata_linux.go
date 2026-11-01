//go:build linux

package ghfixture

import (
	"os"
	"path/filepath"
	"strings"
)

func (c *checks) checkMetadataUpdates() bool {
	path := filepath.Join(c.original, "hosts.yml")
	if !writeFixture(path, c.registrations) {
		return false
	}
	c.ready = filepath.Join(c.root, "metadata-ready")
	c.release = filepath.Join(c.root, "metadata-release")
	alias := `!gh config set git_protocol ssh --host enterprise.two.test;
touch "$GH_TEST_READY";
while test ! -f "$GH_TEST_RELEASE"; do sleep 0.05; done`

	if !c.verify(
		"metadata mutation alias saves",
		c.gh("", nil, "alias", "set", "metadata-pending", alias).code == 0,
	) {
		return false
	}
	p := c.pendingAlias("metadata-pending")
	defer p.stop()
	if !c.verify("tokenless metadata invocation reached mutation", c.waitReady()) {
		return false
	}
	return c.checkConcurrentMetadata(p, path)
}

func (c *checks) checkConcurrentMetadata(p *pendingCommand, path string) bool {
	changed := strings.ReplaceAll(
		c.registrations,
		"user: dotfiles-user",
		"user: parallel-user",
	) + "unrelated.test:\n    git_protocol: ssh\n"
	if !writeFixture(path, changed) || !writeFixture(c.release, "") {
		return false
	}
	completed := p.join()
	return c.checkMetadataResult(completed, path)
}

func (c *checks) checkMetadataResult(completed bool, path string) bool {
	if !c.verify(
		"host-setting delta preserves parallel user and host changes",
		completed && strings.Contains(c.saved(), "user: parallel-user") && strings.Contains(c.saved(), "unrelated.test:") &&
			c.noForwardedSaved(),
	) {
		return false
	}
	if !c.verify(
		"host-setting delta persists at host and account positions",
		strings.Count(c.saved(), "git_protocol: ssh") >= 3,
	) {
		return false
	}
	if !c.verify(
		"metadata alias removed",
		c.gh("", nil, "alias", "delete", "metadata-pending").code == 0,
	) {
		return false
	}
	return os.Remove(path) == nil
}
