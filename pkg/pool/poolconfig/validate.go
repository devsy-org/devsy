package poolconfig

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// maxPoolNameLength keeps "p-<pool>-<10 chars>" within the 48 character
// workspace ID limit.
const maxPoolNameLength = 32

var (
	poolNamePattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	placeholderRegex = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
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
	routes := p.Spec.validateCredentials(&errs)
	p.Spec.validateAgents(&errs, routes)
	p.Spec.validateAudit(&errs)
	return errors.Join(errs...)
}

func validateName(errs *fieldErrors, path, name string, maxLen int) {
	if !poolNamePattern.MatchString(name) || len(name) > maxLen {
		errs.add(path, "must be 1 to %d lowercase letters, digits or dashes, starting and ending "+
			"with a letter or digit", maxLen)
	}
}

func (s *Spec) validateSource(errs *fieldErrors) {
	if s.Source.Repo == "" {
		errs.add("spec.source.repo", "is required")
	}
	path := s.Source.DevcontainerPath
	clean := filepath.ToSlash(filepath.Clean(path))
	if path != "" && (filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, "../")) {
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
	durations := []struct {
		path  string
		value Duration
	}{
		{"spec.lifecycle.spawnTimeout", l.SpawnTimeout},
		{"spec.lifecycle.idleTimeout", l.IdleTimeout},
		{"spec.lifecycle.jobTimeout", l.JobTimeout},
	}
	if l.TTL.Duration <= 0 {
		errs.add("spec.lifecycle.ttl", "must be positive")
	}
	for _, d := range durations {
		if d.value.Duration <= 0 || d.value.Duration > l.TTL.Duration {
			errs.add(d.path, "must be positive and not longer than ttl")
		}
	}
}

func (s *Spec) validateAgents(errs *fieldErrors, routes map[string]string) {
	if len(s.Agents) == 0 {
		errs.add("spec.agents", "must define at least one agent")
	}
	declared := make(map[string]bool, len(s.Credentials))
	for _, c := range s.Credentials {
		declared[c.Name] = true
	}
	for name, agent := range s.Agents {
		path := "spec.agents." + name
		validateName(errs, path, name, maxNameLength)
		if len(agent.Command) == 0 || agent.Command[0] == "" {
			errs.add(path+".command", "is required")
		}
		for _, cred := range agent.Credentials {
			if !declared[cred] {
				errs.add(path+".credentials", "names undeclared credential %q", cred)
			}
		}
		validateAgentEnv(errs, path+".env", agent, routes)
	}
}

func validateAgentEnv(errs *fieldErrors, path string, agent Agent, routes map[string]string) {
	for key, value := range agent.Env {
		if err := validateEnvName(key); err != nil {
			errs.add(path, "%v", err)
		}
		for _, m := range placeholderRegex.FindAllStringSubmatch(value, -1) {
			if msg := checkPlaceholder(m[1], agent, routes); msg != "" {
				errs.add(path+"."+key, "%s", msg)
			}
		}
	}
}

func checkPlaceholder(expr string, agent Agent, routes map[string]string) string {
	routeName, ok := strings.CutPrefix(expr, "route.")
	if !ok {
		return fmt.Sprintf("unknown placeholder %q; only {{route.<name>}} is supported", expr)
	}
	owner, found := routes[routeName]
	if !found {
		return fmt.Sprintf("names undeclared route %q", routeName)
	}
	if !slices.Contains(agent.Credentials, owner) {
		return fmt.Sprintf(
			"uses route %q but the agent does not list credential %q",
			routeName,
			owner,
		)
	}
	return ""
}

func (s *Spec) validateAudit(errs *fieldErrors) {
	if s.Audit.RetainDays < 1 {
		errs.add("spec.audit.retainDays", "must be at least 1")
	}
	if s.Audit.Dir != "" && !filepath.IsAbs(s.Audit.Dir) {
		errs.add("spec.audit.dir", "must be an absolute path")
	}
}
