package dependencies

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	controllerHostScope   = "controller_host"
	maxRuntimeCheckSchema = 1
)

var (
	knownPlatforms = []string{
		platformLinuxAMD64, "linux/arm64", "darwin/amd64", platformDarwinARM64,
		"windows/amd64", "windows/arm64",
	}
	knownOperations   = []string{"configure", "create", "exec", "stop", "delete", "image-build"}
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	optionPattern     = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// validateBinding checks shape and rules that hold for every binding. Which
// provider owns which platform, probe or option is data in v1.json and is pinned
// by tests there; a probe or option name is checked here for form only.
func validateBinding(binding Binding) error {
	if err := validateBindingFields(binding); err != nil {
		return err
	}
	if err := validatePlatforms(binding.Platforms); err != nil {
		return err
	}
	return validateRequirements(binding)
}

func validateBindingFields(binding Binding) error {
	switch {
	case !identifierPattern.MatchString(binding.ID):
		return errors.New("binding id is not a plain identifier")
	case !identifierPattern.MatchString(binding.ExecutionContract):
		return errors.New("execution contract is not a plain identifier")
	case binding.AdapterSemverRange != "":
		return errors.New("no managed adapter range has been released and reviewed")
	case binding.RequiredRuntimeCheckSchema < 0 ||
		binding.RequiredRuntimeCheckSchema > maxRuntimeCheckSchema:
		return errors.New("unrecognized runtime-check schema requirement")
	}
	return validateBindingSource(binding.Source)
}

func validateBindingSource(source ProviderSource) error {
	switch source.Kind {
	case SourceInternal:
		if source.InternalID == "" || source.Repository != "" || source.Location != "" {
			return errors.New("internal source needs only an internal id")
		}
		return nil
	case SourceGitHub:
		return validateGithubSource(source)
	default:
		return fmt.Errorf("source kind %q is not allowed in the built-in catalog", source.Kind)
	}
}

func validateGithubSource(source ProviderSource) error {
	if strings.HasPrefix(source.Repository, "github.com/") &&
		source.InternalID == "" && source.Location == "" {
		return nil
	}
	return errors.New("github source needs only a github.com repository")
}

func validatePlatforms(platforms []string) error {
	if len(platforms) == 0 {
		return errors.New("at least one platform is required")
	}
	for i, platform := range platforms {
		if !slices.Contains(knownPlatforms, platform) {
			return fmt.Errorf("unknown platform %q", platform)
		}
		if slices.Contains(platforms[:i], platform) {
			return fmt.Errorf("duplicate platform %q", platform)
		}
	}
	return nil
}

func validateRequirements(binding Binding) error {
	if len(binding.Requirements) == 0 {
		return errors.New("at least one requirement is required")
	}
	seen := make(map[string]bool)
	for _, requirement := range binding.Requirements {
		if err := validateRequirement(requirement); err != nil {
			return err
		}
		key := requirement.ToolID + ":" + requirement.Purpose
		if seen[key] {
			return fmt.Errorf("duplicate requirement %q", key)
		}
		seen[key] = true
	}
	if binding.GuidanceID != binding.Requirements[0].GuidanceID {
		return errors.New("binding guidance differs from its first requirement")
	}
	return nil
}

func validateRequirement(requirement Requirement) error {
	if !plainIdentifiers(requirement.ToolID, requirement.Purpose, requirement.GuidanceID) {
		return errors.New("requirement tool, purpose and guidance must be plain identifiers")
	}
	if requirement.ProbeID != "" && !plainIdentifiers(requirement.ProbeID) {
		return errors.New("requirement probe is not a plain identifier")
	}
	if requirement.Scope != controllerHostScope {
		return fmt.Errorf("unrecognized requirement scope %q", requirement.Scope)
	}
	if !slices.Contains([]string{"", toolDocker}, requirement.WhenImageBackend) {
		return errors.New("unrecognized image backend condition")
	}
	if err := validateOptions(requirement); err != nil {
		return err
	}
	return validateOperations(requirement.RequiredFor)
}

func plainIdentifiers(values ...string) bool {
	return !slices.ContainsFunc(values, func(value string) bool {
		return !identifierPattern.MatchString(value)
	})
}

func validateOptions(requirement Requirement) error {
	executable, configuration := requirement.ExecutableOption, requirement.ConfigurationOption
	if executable != "" && configuration != "" {
		return errors.New("requirement names both an executable and a configuration option")
	}
	for _, option := range []string{executable, configuration} {
		if option != "" && !optionPattern.MatchString(option) {
			return fmt.Errorf("option %q is not an upper-case option name", option)
		}
	}
	return nil
}

func validateOperations(operations []string) error {
	if len(operations) == 0 {
		return errors.New("requirement operations are required")
	}
	for i, operation := range operations {
		if !slices.Contains(knownOperations, operation) {
			return fmt.Errorf("unrecognized operation %q", operation)
		}
		if slices.Contains(operations[:i], operation) {
			return fmt.Errorf("duplicate operation %q", operation)
		}
	}
	return nil
}
