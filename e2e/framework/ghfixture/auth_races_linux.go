//go:build linux

package ghfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"gopkg.in/yaml.v3"
)

const (
	refreshOperation = "refresh"
	switchOperation  = "switch"
	logoutOperation  = "logout"
	loginOperation   = "login"
	authRaceAlias    = "auth-pending"
)

type authRace struct{ first, second string }

type (
	raceAccount struct {
		Token string `yaml:"oauth_token,omitempty"`
	}
	raceHost struct {
		User     string                 `yaml:"user,omitempty"`
		Token    string                 `yaml:"oauth_token,omitempty"`
		Users    map[string]raceAccount `yaml:"users,omitempty"`
		Protocol string                 `yaml:"git_protocol,omitempty"`
	}
)

func (c *checks) checkAuthenticationRaces() bool {
	path := filepath.Join(c.original, "hosts.yml")
	before, err := readFixture(path)
	defer restoreFixture(path, before, err)
	config, configErr := readFixture(c.target)
	defer restoreFixture(c.target, config, configErr)
	env := c.env
	c.env = overlayEnvironment(env, map[string]string{envGHBrowser: "/bin/true"})
	defer func() { c.env = env }()
	for _, mode := range []string{"all-local", "mixed"} {
		for _, race := range []authRace{
			{refreshOperation, switchOperation},
			{switchOperation, refreshOperation},
			{refreshOperation, logoutOperation},
			{logoutOperation, refreshOperation},
			{loginOperation, logoutOperation},
			{logoutOperation, loginOperation},
		} {
			if !c.checkAuthenticationRace(mode, race) {
				return false
			}
		}
	}
	if !c.checkIndependentHostRace() || !c.checkIndependentKeyringRace() ||
		!c.checkLogoutRecovery() || !c.checkMalformedLatestHostsRecovery() {
		return false
	}
	return c.gh("", nil, "alias", "delete", authRaceAlias).code == 0
}

func (c *checks) checkAuthenticationRace(mode string, race authRace) bool {
	prefix := "auth race " + mode + " " + race.first + " then " + race.second + ": "
	base, ok := c.prepareAuthenticationRace(mode, race)
	if !c.verify(prefix+"alias prepared", ok) {
		return false
	}
	p := c.pendingAlias(authRaceAlias)
	defer p.stop()
	if !c.verify(prefix+"mutation reached barrier", c.waitReady()) {
		return false
	}
	parallel := c.mutateAuthentication(race.second, Hosts[0])
	if !c.verify(prefix+"parallel mutation succeeds", parallel.code == 0) {
		return false
	}
	if !writeFixture(c.release, "") {
		return false
	}
	completed := p.join()
	c.stderr = append(c.stderr, p.stderr.String())
	if !c.verify(prefix+"pending command preserves exit", completed) {
		return false
	}
	return c.checkAuthenticationRaceResult(prefix, race, base, p)
}

func (c *checks) checkAuthenticationRaceResult(
	prefix string,
	race authRace,
	base map[string]raceHost,
	p *pendingCommand,
) bool {
	if !c.verify(
		prefix+"conflict reported",
		strings.Contains(p.stderr.String(), "authentication_conflict"),
	) {
		return false
	}
	expected := c.expectedAuthentication(base[Hosts[0]], race.second, Hosts[0])
	if !c.verify(prefix+"API identity preserved", c.originalIdentity(Hosts[0], expected.User)) {
		return false
	}
	base[Hosts[0]] = expected
	if !c.verify(
		prefix+"complete authentication metadata preserved",
		c.authenticationMatches(base),
	) {
		return false
	}
	return c.verify(prefix+"forwarded credentials absent from artifacts", c.authArtifactsClean())
}

func (c *checks) prepareAuthenticationRace(mode string, race authRace) (map[string]raceHost, bool) {
	c.ready = filepath.Join(c.root, "auth-race-ready")
	c.release = filepath.Join(c.root, "auth-race-release")
	_ = os.Remove(c.ready)
	_ = os.Remove(c.release)
	base := c.authenticationRaceBase(mode, race)
	// #nosec G117 -- Generated dummy accounts seed the disposable CLI configuration.
	data, err := yaml.Marshal(base)
	if err != nil || !writeFixture(filepath.Join(c.original, "hosts.yml"), string(data)) {
		return nil, false
	}
	if !writeFixture(filepath.Join(c.root, "auth-race-token"), c.accounts[Hosts[0]].Second) {
		return nil, false
	}
	return base, c.setAuthenticationAlias(race.first, "")
}

func (c *checks) setAuthenticationAlias(operation, editor string) bool {
	command := shellescape.QuoteCommand(
		append([]string{"gh"}, authenticationArguments(operation, Hosts[0])...),
	)
	if operation == loginOperation {
		command += " < " + shellescape.Quote(filepath.Join(c.root, "auth-race-token"))
	}
	if editor != "" {
		command += " && " + shellescape.QuoteCommand(
			[]string{"gh", "config", "set", "editor", editor},
		)
	}
	alias := "!" + command + ` && touch "$GH_TEST_READY" && while test ! -f "$GH_TEST_RELEASE"; do sleep 0.05; done`
	return c.gh("", nil, "alias", "set", "--clobber", authRaceAlias, alias).code == 0
}

func authenticationArguments(operation, host string) []string {
	args := []string{"auth", operation, "--hostname", host}
	switch operation {
	case refreshOperation:
		return append(args, "--insecure-storage")
	case switchOperation:
		return append(args, "--user", secondUser)
	case logoutOperation:
		return append(args, "--user", hostUser)
	case loginOperation:
		return append(args, "--with-token", "--insecure-storage")
	default:
		return nil
	}
}

func (c *checks) mutateAuthentication(operation, host string) processResult {
	input := ""
	if operation == loginOperation {
		input = c.accounts[host].Second
	}
	return c.gh(input, nil, authenticationArguments(operation, host)...)
}

func (c *checks) expectedAuthentication(base raceHost, operation, host string) raceHost {
	users := map[string]raceAccount{}
	maps.Copy(users, base.Users)
	base.Users = users
	switch operation {
	case refreshOperation:
		base.User, base.Token = hostUser, c.accounts[host].Refresh
	case switchOperation, loginOperation:
		base.User, base.Token = secondUser, c.accounts[host].Second
	case logoutOperation:
		return raceHost{}
	}
	base.Users[base.User] = raceAccount{Token: base.Token}
	return base
}

func (c *checks) originalIdentity(host, user string) bool {
	result := execute(
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
	c.last = result
	c.stderr = append(c.stderr, result.stderr)
	if user == "" {
		return result.code != 0 &&
			(strings.Contains(result.stderr, "HTTP 401") || strings.Contains(result.stderr, "gh auth login"))
	}
	return result.code == 0 && strings.TrimSpace(result.stdout) == user
}

func (c *checks) authenticationMatches(expected map[string]raceHost) bool {
	actual := map[string]raceHost{}
	if yaml.Unmarshal([]byte(c.saved()), &actual) != nil {
		return false
	}
	for _, host := range Hosts {
		a, e := actual[host], expected[host]
		a.Protocol, e.Protocol = "", ""
		if len(a.Users) == 0 {
			a.Users = nil
		}
		if len(e.Users) == 0 {
			e.Users = nil
		}
		if !reflect.DeepEqual(a, e) {
			return false
		}
	}
	return true
}

func (c *checks) authenticationRaceBase(mode string, race authRace) map[string]raceHost {
	base := map[string]raceHost{}
	for _, host := range Hosts {
		if host == Hosts[0] || mode == "all-local" {
			token := localRaceToken(c.accounts[host])
			base[host] = raceHost{
				User:     hostUser,
				Token:    token,
				Users:    map[string]raceAccount{hostUser: {Token: token}},
				Protocol: "https",
			}
		}
	}
	if race.first == switchOperation || race.second == switchOperation {
		host := base[Hosts[0]]
		host.Users[secondUser] = raceAccount{Token: c.accounts[Hosts[0]].Second}
	}
	return base
}

func localRaceToken(account Tokens) string {
	fingerprint := sha256.Sum256([]byte("local-auth-race:" + account.Forwarded))
	return "ghp_" + hex.EncodeToString(fingerprint[:])[:40]
}

func (c *checks) authArtifactsClean() bool {
	for _, root := range []string{c.original, filepath.Join(c.root, "guest-cache", "devsy", "gh-relay")} {
		if filepath.WalkDir(root, c.checkAuthArtifact) != nil {
			return false
		}
	}
	return true
}

func (c *checks) checkAuthArtifact(path string, entry os.DirEntry, err error) error {
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || entry.IsDir() {
		return err
	}
	target, resolveErr := filepath.EvalSymlinks(path)
	if resolveErr != nil {
		return resolveErr
	}
	b, readErr := readFixture(target)
	if readErr != nil {
		return readErr
	}
	for _, account := range c.accounts {
		if strings.Contains(string(b), account.Forwarded) {
			return os.ErrInvalid
		}
	}
	return nil
}
