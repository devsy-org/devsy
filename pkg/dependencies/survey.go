package dependencies

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	surveyUnknown             = "unknown"
	surveyMissing             = "missing"
	surveyAvailable           = "available"
	surveyIncomplete          = "incomplete"
	surveyNotChecked          = "not_checked"
	surveyExternalOnly        = "external_only"
	surveyPolicyBlocked       = "dependency_policy_blocked"
	surveyVersionUnknown      = "dependency_version_unknown"
	surveyUnsupported         = "unsupported"
	surveyPlatformUnsupported = "dependency_platform_unsupported"
)

// Survey matches the trusted contract and observes its executable requirements
// through the supplied native lookup snapshot. It never executes, selects,
// installs, reads runtime configuration files, or checks an endpoint. Any error,
// including cancellation, discards the whole result.
func (c *Catalog) Survey(ctx context.Context, request SurveyRequest) (SurveyResult, error) {
	if err := ctx.Err(); err != nil {
		return SurveyResult{}, err
	}
	if err := validateSurvey(c, request); err != nil {
		return SurveyResult{}, err
	}
	match, err := c.Match(request.Contract)
	if err != nil {
		// Catalog validation errors can contain caller-supplied contract values.
		return SurveyResult{}, errors.New("invalid survey provider contract")
	}
	result := SurveyResult{
		Target: request.Target, Host: request.Host,
		CatalogID: c.ID(), CatalogHash: c.Hash(), BindingID: match.BindingID,
		ContractVerified: match.ContractVerified, AdapterStatus: match.AdapterStatus,
		PolicyReason: c.surveyPolicyReason(match, runtime.GOOS+"/"+runtime.GOARCH),
	}
	for _, requirement := range match.Requirements {
		item, err := surveyRequirement(ctx, request, requirement)
		if err != nil {
			return SurveyResult{}, err
		}
		item.PolicyReason = result.PolicyReason
		if item.PolicyReason == surveyPlatformUnsupported {
			item.Policy = surveyUnsupported
		}
		result.Requirements = append(result.Requirements, item)
	}
	if err := ctx.Err(); err != nil {
		return SurveyResult{}, err
	}
	return result, nil
}

func (c *Catalog) surveyPolicyReason(match ContractMatch, platform string) string {
	if !match.ContractVerified {
		return match.ManagedReason
	}
	for _, binding := range c.document.Bindings {
		if binding.ID == match.BindingID && !slices.Contains(binding.Platforms, platform) {
			return surveyPlatformUnsupported
		}
	}
	return match.ManagedReason
}

func validateSurvey(c *Catalog, request SurveyRequest) error {
	if c == nil || c.hash == "" {
		return errors.New("survey requires a loaded catalog")
	}
	if !filepath.IsAbs(request.Environment.cwd) {
		return errors.New("survey requires an environment snapshot")
	}
	if request.Host.GOOS != "" && request.Host.GOOS != runtime.GOOS {
		return errors.New("survey cannot inspect a different operating system")
	}
	if request.Host.GOARCH != "" && request.Host.GOARCH != runtime.GOARCH {
		return errors.New("survey cannot inspect a different process architecture")
	}
	return nil
}

func surveyRequirement(
	ctx context.Context,
	request SurveyRequest,
	requirement Requirement,
) (RequirementSurvey, error) {
	item := RequirementSurvey{
		Requirement: requirement, Availability: surveyUnknown,
		Compatibility: surveyUnknown, Reachability: surveyNotChecked,
		Policy: surveyExternalOnly, ReasonCode: surveyPolicyBlocked,
	}
	if requirement.ConfigurationOption != "" {
		// Configuration guidance never parses kubeconfig or invokes exec auth.
		return item, nil
	}
	lookup, err := surveyLookupRequest(request, requirement)
	if err != nil {
		return RequirementSurvey{}, err
	}
	var observed LookupResult
	if surveyURI(lookup.Command) {
		observed.ReasonCode = LookupInvalidPath
	} else {
		observed, err = request.Environment.LookupExecutable(ctx, lookup)
		if err != nil {
			return RequirementSurvey{}, err
		}
	}
	item.Candidates = observed.Candidates
	item.Availability = surveyAvailability(observed)
	item.ReasonCode = surveyObservationReason(observed.ReasonCode, item.Availability)
	for i := range item.Candidates {
		if item.Candidates[i].ReasonCode == LookupInspectionDeferred {
			item.Candidates[i].ReasonCode = surveyPolicyBlocked
		}
	}
	return item, nil
}

func surveyURI(command string) bool {
	prefix, _, found := strings.Cut(command, "://")
	// Native absolute and relative invocation paths retain their exact spelling.
	return found && !filepath.IsAbs(command) && !invocationPath(prefix)
}

func surveyLookupRequest(request SurveyRequest, requirement Requirement) (LookupRequest, error) {
	command, ok := surveyDefaultCommand(requirement.ToolID)
	if !ok {
		return LookupRequest{}, errors.New("survey requirement has no reviewed executable")
	}
	lookup := LookupRequest{
		Command:           command,
		ReviewedLocations: slices.Clone(request.ReviewedLocations[requirement.ToolID]),
	}
	option, present := request.Options[requirement.ExecutableOption]
	if requirement.ExecutableOption == "" || !present {
		return lookup, nil
	}
	return applySurveyOption(lookup, option)
}

func applySurveyOption(lookup LookupRequest, option SurveyOption) (LookupRequest, error) {
	origin, err := surveyOptionOrigin(option)
	if err != nil {
		return LookupRequest{}, err
	}
	// Non-default static values are fixed choices too, even if inherited without
	// UserProvided. Empty values must never become permission to search elsewhere.
	lookup.Explicit = origin != SurveyDefault || option.UserProvided ||
		option.Value != lookup.Command
	lookup.Command = option.Value
	if lookup.Explicit {
		lookup.ExplicitOrigin = LookupExplicit
		if origin == LookupExplicitEnv {
			lookup.ExplicitOrigin = origin
		}
	}
	return lookup, nil
}

func surveyOptionOrigin(option SurveyOption) (LookupOrigin, error) {
	switch option.Origin {
	case "", SurveyDefault:
		return SurveyDefault, nil
	case LookupExplicit, LookupExplicitEnv:
		return option.Origin, nil
	default:
		return "", errors.New("invalid survey executable option origin")
	}
}

func surveyDefaultCommand(toolID string) (string, bool) {
	// These identities are validated by the embedded catalog, not provider YAML.
	switch toolID {
	case toolDocker, "podman":
		return toolID, true
	case "apple-container":
		return "container", true
	case toolMicrosandbox:
		return "msb", true
	case toolLima:
		return "limactl", true
	case toolOrbStack:
		return "orbctl", true
	default:
		return "", false
	}
}

func surveyObservationReason(reason, availability string) string {
	switch reason {
	case "":
		return surveyVersionUnknown
	case LookupInspectionDeferred:
		return surveyPolicyBlocked
	case LookupMissing:
		return surveyUnavailableReason(availability)
	default:
		return reason
	}
}

func surveyUnavailableReason(availability string) string {
	switch availability {
	case surveyIncomplete:
		return "dependency_incomplete"
	case surveyUnknown:
		return surveyPolicyBlocked
	default:
		return LookupMissing
	}
}
