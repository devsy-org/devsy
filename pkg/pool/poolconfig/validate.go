package poolconfig

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/secrets"
)

const (
	// maxPoolNameLength keeps "p-<pool>-<10 chars>" within the 48 character
	// workspace ID limit.
	maxPoolNameLength = 32
	maxNameLength     = 63

	errRepoCredentials = "must not contain credentials; use a credential grant"
	errRepoURL         = "must be a valid repository URL"
	errRepoQuery       = "must not contain a query or fragment"
	scpPrefix          = "git@"
	errEnvName         = "names must start with a letter or underscore and contain only " +
		"letters, digits and underscores"
)

var (
	namePattern       = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	rootedPathPattern = regexp.MustCompile(`^([A-Za-z]:|/)`)
	sshUserPattern    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	// reservedEnvNames are set by the pool or would break credential delivery,
	// egress or TLS trust.
	reservedEnvNames = []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
		"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST",
		"GH_CONFIG_DIR", "GITHUB_API_URL", "GITHUB_SERVER_URL",
		"GIT_ASKPASS", "SSH_ASKPASS", "GIT_PROXY_COMMAND", "GIT_SSH_COMMAND", "GIT_SSH",
		"GIT_EXEC_PATH", "SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE",
		"NODE_EXTRA_CA_CERTS", "NODE_TLS_REJECT_UNAUTHORIZED",
		"DEVSY_SESSION_ID",
	}
	reservedEnvPrefixes = []string{"GIT_CONFIG", "GIT_SSL"}
)

type fieldError struct {
	at  fieldPath
	msg string
}

// fieldErrors collects every problem so one validate run reports them all.
type fieldErrors []fieldError

func (e *fieldErrors) add(path, format string, args ...any) {
	e.addAt(fieldPath{key: path, shown: path}, format, args...)
}

func (e *fieldErrors) addAt(at fieldPath, format string, args ...any) {
	*e = append(*e, fieldError{at: at, msg: fmt.Sprintf(format, args...)})
}

// join adds the source line of each path that has one.
func (e fieldErrors) join(lines map[string]int) error {
	errs := make([]error, 0, len(e))
	for _, f := range e {
		if line, ok := lines[f.at.key]; ok {
			errs = append(errs, fmt.Errorf("%s (line %d): %s", f.at.shown, line, f.msg))
		} else {
			errs = append(errs, fmt.Errorf("%s: %s", f.at.shown, f.msg))
		}
	}
	return errors.Join(errs...)
}

// Validate checks a defaulted pool and returns every problem found.
func (p *Pool) Validate() error {
	return p.validate(nil)
}

func (p *Pool) validate(lines map[string]int) error {
	var errs fieldErrors
	if p.APIVersion != APIVersion {
		errs.add("apiVersion", "must be %q", APIVersion)
	}
	if p.Kind != Kind {
		errs.add("kind", "must be %q", Kind)
	}
	validateName(&errs, "metadata.name", p.Metadata.Name, maxPoolNameLength)
	p.Spec.validateSource(&errs)
	p.Spec.validateCapacity(&errs)
	p.Spec.validateLifecycle(&errs)
	p.Spec.validateAgents(&errs)
	p.Spec.validateAudit(&errs)
	return errs.join(lines)
}

func validateName(errs *fieldErrors, path, name string, maxLen int) {
	validateNameAt(errs, fieldPath{key: path, shown: path}, name, maxLen)
}

func validateNameAt(errs *fieldErrors, at fieldPath, name string, maxLen int) {
	if !namePattern.MatchString(name) || len(name) > maxLen {
		errs.addAt(at, "must be 1 to %d lowercase letters, digits or dashes, starting and "+
			"ending with a letter or digit", maxLen)
	}
}

// escapesRepo also refuses rooted and drive paths, which filepath.IsAbs
// accepts as relative on Windows, and backslashes, which name a different
// file on Linux than on Windows.
func escapesRepo(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return strings.Contains(path, `\`) || filepath.IsAbs(path) ||
		rootedPathPattern.MatchString(path) || clean == ".." || strings.HasPrefix(clean, "../")
}

// repoError finds a token pasted into the repository reference, which would
// otherwise reach the policy hash, audit records and logs. It checks the
// reference the way workspaces read it, after git.NormalizeRepository strips
// git: and the @branch suffix and reads a bare host as https, so
// user@host/repo carries https userinfo. Only an ssh login name such as git
// may come before an @. Messages never include the value.
func repoError(repo string) string {
	normalized := git.NormalizeRepository(repo).Repository
	if rest, ok := strings.CutPrefix(normalized, scpPrefix); ok {
		return scpError(repo, rest)
	}
	// The raw text is checked too: NormalizeRepository reads
	// https://user:token@host/a as repository https://user:token and branch
	// host/a.
	if hasURLCredentials(normalized, true) ||
		hasURLCredentials(strings.TrimPrefix(repo, "git:"), false) {
		return errRepoCredentials
	}
	if strings.ContainsAny(repo, "?#") {
		return errRepoQuery
	}
	if u, err := url.Parse(normalized); err != nil || (u.Host == "" && u.Scheme != "file") {
		return errRepoURL
	}
	return ""
}

// hasURLCredentials reads the text directly, since a token can make the
// reference fail to parse as a URL. An @ left in the path of a normalized
// reference means a nested or mis-cased prefix, such as
// git:git:https://user@host or SSH://user@host.
func hasURLCredentials(ref string, checkPath bool) bool {
	scheme, rest, found := strings.Cut(ref, "://")
	if !found {
		scheme, rest = "https", ref
	}
	authority, path, _ := strings.Cut(rest, "/")
	at := strings.LastIndexByte(authority, '@')
	return (checkPath && strings.Contains(path, "@")) ||
		(at >= 0 && (scheme != "ssh" || !sshUserPattern.MatchString(authority[:at])))
}

// scpError checks host:path after the git@ of an scp-style reference.
func scpError(repo, rest string) string {
	if strings.Contains(rest, "@") {
		return errRepoCredentials
	}
	if strings.ContainsAny(repo, "?#") {
		return errRepoQuery
	}
	host, path, ok := strings.Cut(rest, ":")
	if !ok || host == "" || path == "" {
		return errRepoURL
	}
	return ""
}

func (s *Spec) validateSource(errs *fieldErrors) {
	if s.Source.Repo == "" {
		errs.add("spec.source.repo", "is required")
	} else if msg := repoError(s.Source.Repo); msg != "" {
		errs.add("spec.source.repo", "%s", msg)
	}
	if path := s.Source.DevcontainerPath; path != "" && escapesRepo(path) {
		errs.add(
			"spec.source.devcontainerPath",
			"must be a relative path with / separators that stays inside the repository",
		)
	}
	if s.Provider.Name == "" {
		errs.add("spec.provider.name", "is required")
	}
}

func (s *Spec) validateCapacity(errs *fieldErrors) {
	c := s.Capacity
	if c.MaxConcurrent < 1 {
		errs.add("spec.capacity.maxConcurrent", "must be at least 1")
	}
	if c.MaxQueued < 0 {
		errs.add("spec.capacity.maxQueued", "must not be negative")
	}
	if c.Warm < 0 || c.Warm > c.MaxConcurrent {
		errs.add("spec.capacity.warm", "must be between 0 and maxConcurrent")
	}
}

func (s *Spec) validateLifecycle(errs *fieldErrors) {
	l := s.Lifecycle
	if l.TTL.Duration <= 0 {
		errs.add("spec.lifecycle.ttl", "must be positive")
	}
	checks := []struct {
		path  string
		value Duration
	}{
		{"spec.lifecycle.spawnTimeout", l.SpawnTimeout},
		{"spec.lifecycle.idleTimeout", l.IdleTimeout},
		{"spec.lifecycle.jobTimeout", l.JobTimeout},
	}
	for _, c := range checks {
		if c.value.Duration <= 0 || c.value.Duration > l.TTL.Duration {
			errs.add(c.path, "must be positive and not longer than ttl")
		}
	}
}

func (s *Spec) validateAgents(errs *fieldErrors) {
	if len(s.Agents) == 0 {
		errs.add("spec.agents", "must define at least one agent")
	}
	for name, agent := range s.Agents {
		path := agentPath(name)
		validateNameAt(errs, path, name, maxNameLength)
		if len(agent.Command) == 0 || agent.Command[0] == "" {
			errs.addAt(path.field("command"), "is required")
		}
		for key := range agent.Env {
			switch {
			case secrets.ValidateName(key) != nil:
				errs.addAt(envPath(path, key), errEnvName)
			case isReservedEnvName(key):
				errs.addAt(reservedEnvPath(path, key),
					"is set by the pool and cannot be overridden")
			}
		}
	}
}

// agentPath names an agent only when its name is valid; an invalid name may
// be pasted text.
func agentPath(name string) fieldPath {
	label := hiddenKey
	if isValidName(name) {
		label = name
	}
	return fieldPath{key: "spec.agents", shown: "spec.agents"}.entry(name, label)
}

// reservedEnvPath shows only an exact reserved name, which cannot be a
// secret; a name that matched a prefix may carry pasted text after it.
func reservedEnvPath(agent fieldPath, key string) fieldPath {
	if slices.Contains(reservedEnvNames, strings.ToUpper(key)) {
		return agent.field("env").entry(key, key)
	}
	return envPath(agent, key)
}

// envPath never shows the name: a token can look like a valid env name.
func envPath(agent fieldPath, key string) fieldPath {
	return agent.field("env").entry(key, hiddenKey)
}

func isValidName(name string) bool {
	return namePattern.MatchString(name) && len(name) <= maxNameLength
}

func isReservedEnvName(name string) bool {
	upper := strings.ToUpper(name)
	return slices.Contains(reservedEnvNames, upper) ||
		slices.ContainsFunc(reservedEnvPrefixes, func(p string) bool {
			return strings.HasPrefix(upper, p)
		})
}

func (s *Spec) validateAudit(errs *fieldErrors) {
	if s.Audit.RetainDays < 1 {
		errs.add("spec.audit.retainDays", "must be at least 1")
	}
	if s.Audit.Dir != "" && !filepath.IsAbs(s.Audit.Dir) {
		errs.add("spec.audit.dir", "must be an absolute path")
	}
}
