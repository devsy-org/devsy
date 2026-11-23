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
	schemeSSH          = "ssh"
	schemeSCP          = "scp"
)

var (
	namePattern       = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	rootedPathPattern = regexp.MustCompile(`^([A-Za-z]:|/)`)
	schemePattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)
	// reservedEnvNames are set by the pool or would break credential delivery
	// or egress.
	reservedEnvNames = []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
		"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST",
		"GIT_ASKPASS", "SSH_ASKPASS", "GIT_PROXY_COMMAND", "GIT_SSH_COMMAND",
		"DEVSY_SESSION_ID",
	}
)

type fieldError struct {
	path, msg string
}

// fieldErrors collects every problem so one validate run reports them all.
type fieldErrors []fieldError

func (e *fieldErrors) add(path, format string, args ...any) {
	*e = append(*e, fieldError{path: path, msg: fmt.Sprintf(format, args...)})
}

// join adds the source line of each path that has one.
func (e fieldErrors) join(lines map[string]int) error {
	errs := make([]error, 0, len(e))
	for _, f := range e {
		if line, ok := lines[f.path]; ok {
			errs = append(errs, fmt.Errorf("%s (line %d): %s", f.path, line, f.msg))
		} else {
			errs = append(errs, fmt.Errorf("%s: %s", f.path, f.msg))
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
	p.Spec.validateEgress(&errs)
	p.Spec.validateAgents(&errs)
	p.Spec.validateAudit(&errs)
	return errs.join(lines)
}

func validateName(errs *fieldErrors, path, name string, maxLen int) {
	if !namePattern.MatchString(name) || len(name) > maxLen {
		errs.add(path, "must be 1 to %d lowercase letters, digits or dashes, starting and ending "+
			"with a letter or digit", maxLen)
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
// otherwise reach the policy hash, audit records and logs. A reference without
// a scheme is read the way workspaces read it, as https, so user@host/repo
// carries https userinfo. A plain ssh user such as git@ is not a secret.
// Messages never include the value.
func repoError(repo string) string {
	ref := strings.TrimPrefix(repo, "git:")
	scheme, rest := splitScheme(ref)
	authority, _, _ := strings.Cut(rest, "/")
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		if strings.Contains(authority[:at], ":") || (scheme != schemeSSH && scheme != schemeSCP) {
			return errRepoCredentials
		}
	}
	if scheme == schemeSCP {
		return ""
	}
	if _, err := url.Parse(git.NormalizeRepository(ref).Repository); err != nil {
		return "must be a valid repository URL"
	}
	return ""
}

// splitScheme reports git@host:path as scp, which uses the ssh user rules.
func splitScheme(ref string) (string, string) {
	if strings.HasPrefix(ref, "git@") {
		return schemeSCP, ref
	}
	scheme, rest, found := strings.Cut(ref, "://")
	if !found || !schemePattern.MatchString(scheme) {
		return "https", ref
	}
	return strings.ToLower(scheme), rest
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
		validateName(errs, path, name, maxNameLength)
		if len(agent.Command) == 0 || agent.Command[0] == "" {
			errs.add(path+".command", "is required")
		}
		for key := range agent.Env {
			if secrets.ValidateName(key) != nil {
				errs.add(path+".env", "names must start with a letter or underscore and "+
					"contain only letters, digits and underscores")
			} else if isReservedEnvName(key) {
				errs.add(path+".env."+key, "is set by the pool and cannot be overridden")
			}
		}
	}
}

// agentPath names an agent only when its name is valid; an invalid name may
// be pasted text.
func agentPath(name string) string {
	if isValidName(name) {
		return "spec.agents." + name
	}
	return "spec.agents." + hiddenKey
}

func isValidName(name string) bool {
	return namePattern.MatchString(name) && len(name) <= maxNameLength
}

func isReservedEnvName(name string) bool {
	upper := strings.ToUpper(name)
	return slices.Contains(reservedEnvNames, upper) || strings.HasPrefix(upper, "GIT_CONFIG")
}

func (s *Spec) validateAudit(errs *fieldErrors) {
	if s.Audit.RetainDays < 1 {
		errs.add("spec.audit.retainDays", "must be at least 1")
	}
	if s.Audit.Dir != "" && !filepath.IsAbs(s.Audit.Dir) {
		errs.add("spec.audit.dir", "must be an absolute path")
	}
}
