package poolconfig

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/secrets"
)

const (
	// maxPoolNameLength keeps "p-<pool>-<10 chars>" within the 48 character
	// workspace ID limit.
	maxPoolNameLength = 32
	maxNameLength     = 63
)

var (
	namePattern       = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	rootedPathPattern = regexp.MustCompile(`^([A-Za-z]:|[/\\])`)
	// reservedEnvNames are set by the pool or would break credential delivery
	// or egress.
	reservedEnvNames = []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
		"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST",
		"GIT_ASKPASS", "SSH_ASKPASS", "GIT_PROXY_COMMAND", "GIT_SSH_COMMAND",
		"DEVSY_SESSION_ID",
	}
)

// fieldErrors collects every problem so one validate run reports them all.
type fieldErrors []error

func (e *fieldErrors) add(path, format string, args ...any) {
	*e = append(*e, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
}

// Validate checks a defaulted pool and returns every problem found.
func (p *Pool) Validate() error {
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
	return errors.Join(errs...)
}

func validateName(errs *fieldErrors, path, name string, maxLen int) {
	if !namePattern.MatchString(name) || len(name) > maxLen {
		errs.add(path, "must be 1 to %d lowercase letters, digits or dashes, starting and ending "+
			"with a letter or digit", maxLen)
	}
}

// escapesRepo also refuses rooted and drive paths, which filepath.IsAbs
// accepts as relative on Windows.
func escapesRepo(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(path, `\`, "/")))
	return filepath.IsAbs(path) || rootedPathPattern.MatchString(path) ||
		clean == ".." || strings.HasPrefix(clean, "../")
}

func (s *Spec) validateSource(errs *fieldErrors) {
	if s.Source.Repo == "" {
		errs.add("spec.source.repo", "is required")
	}
	if path := s.Source.DevcontainerPath; path != "" && escapesRepo(path) {
		errs.add(
			"spec.source.devcontainerPath",
			"must be relative to the repository and stay inside it",
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
		path := "spec.agents." + name
		validateName(errs, path, name, maxNameLength)
		if len(agent.Command) == 0 || agent.Command[0] == "" {
			errs.add(path+".command", "is required")
		}
		for key := range agent.Env {
			if err := validateEnvName(key); err != nil {
				errs.add(path+".env", "%v", err)
			}
		}
	}
}

func validateEnvName(name string) error {
	if err := secrets.ValidateName(name); err != nil {
		return err
	}
	upper := strings.ToUpper(name)
	if slices.Contains(reservedEnvNames, upper) || strings.HasPrefix(upper, "GIT_CONFIG") {
		return fmt.Errorf("%s is set by the pool and cannot be overridden", name)
	}
	return nil
}

func (s *Spec) validateAudit(errs *fieldErrors) {
	if s.Audit.RetainDays < 1 {
		errs.add("spec.audit.retainDays", "must be at least 1")
	}
	if s.Audit.Dir != "" && !filepath.IsAbs(s.Audit.Dir) {
		errs.add("spec.audit.dir", "must be an absolute path")
	}
}
