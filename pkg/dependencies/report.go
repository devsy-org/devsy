package dependencies

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	reportInvalid          = "invalid passive dependency report input"
	reportPodman           = "podman"
	reportKubernetes       = "kubernetes"
	reportIndirectLauncher = "indirect_launcher"
)

// FormatPassiveReport converts trusted orchestration evidence without effects.
// Catalog identity and ContractVerified are consistency checks, not proof of an
// arbitrary caller-authored SurveyResult. Reobservation is required before use.
func (c *Catalog) FormatPassiveReport(
	result SurveyResult,
	metadata PassiveReportMetadata,
) (Report, error) {
	binding, err := c.passiveReportBinding(result)
	if err != nil {
		return Report{}, err
	}
	if !validReportMetadata(result, metadata) || !validReportSurvey(result, binding) {
		return Report{}, errors.New(reportInvalid)
	}
	item := formatReportItem(result.Requirements[0], metadata)
	return Report{
		Schema:        "devsy.dependencies.report.v1",
		SchemaVersion: 1,
		SurveyMode:    "passive",
		Target: ReportTarget{
			ContextName:            result.Target.ContextName,
			ProviderName:           result.Target.ProviderName,
			ProviderType:           binding.Source.InternalID,
			ProviderSourceID:       "internal:" + binding.Source.InternalID,
			ProviderSourceKind:     "builtin_internal",
			TrustedBuiltinIdentity: true,
			ConfigRevision:         result.Target.ConfigRevision,
			ProviderConfigRevision: result.Target.ProviderConfigRevision,
			EvaluatedAt:            metadata.EvaluatedAt.UTC().Format(time.RFC3339Nano),
		},
		Host: ReportHost{
			GOOS: result.Host.GOOS, GOARCH: result.Host.GOARCH, OSVersion: metadata.OSVersion,
			VirtualizationCapability: metadata.VirtualizationCapability,
		},
		Availability:  item.Availability,
		Compatibility: surveyUnknown,
		Reachability:  surveyNotChecked,
		Policy:        item.Policy,
		ReasonCode:    item.ReasonCode,
		Message:       "Passive inspection does not establish executable identity, version, compatibility, or readiness.",
		Items:         []ReportItem{item},
		Actions:       []ReportAction{},
	}, nil
}

func (c *Catalog) passiveReportBinding(result SurveyResult) (Binding, error) {
	if c == nil || c.hash == "" {
		return Binding{}, errors.New(reportInvalid)
	}
	if !c.validReportCatalog(result) {
		return Binding{}, errors.New(reportInvalid)
	}
	for _, binding := range c.document.Bindings {
		if binding.ID == result.BindingID && binding.Source.Kind == SourceInternal {
			return binding, nil
		}
	}
	return Binding{}, errors.New(reportInvalid)
}

func (c *Catalog) validReportCatalog(result SurveyResult) bool {
	return c.document.validate() == nil && result.CatalogID == c.ID() &&
		result.CatalogHash == c.Hash()
}

func validReportMetadata(result SurveyResult, metadata PassiveReportMetadata) bool {
	if metadata.Redact == nil || metadata.EvaluatedAt.IsZero() {
		return false
	}
	for _, value := range []string{
		result.Target.ContextName, result.Target.ProviderName,
		result.Target.ConfigRevision, result.Target.ProviderConfigRevision,
		result.Host.GOOS, result.Host.GOARCH, metadata.VirtualizationCapability,
	} {
		if !safeReportIdentity(value, metadata.Redact) {
			return false
		}
	}
	if metadata.OSVersion != "" && !safeReportIdentity(metadata.OSVersion, metadata.Redact) {
		return false
	}
	return validReportEvaluation(metadata) && validReportHost(result.Host, metadata)
}

func validReportEvaluation(metadata PassiveReportMetadata) bool {
	evaluatedAt := metadata.EvaluatedAt.UTC().Format(time.RFC3339Nano)
	_, err := time.Parse(time.RFC3339Nano, evaluatedAt)
	return err == nil && metadata.Redact(evaluatedAt) == evaluatedAt
}

func validReportHost(host SurveyHost, metadata PassiveReportMetadata) bool {
	if !slices.Contains([]string{"linux", "darwin", "windows"}, host.GOOS) ||
		!slices.Contains([]string{"amd64", "arm64"}, host.GOARCH) {
		return false
	}
	if !slices.Contains([]string{
		"kvm_available", "kvm_unavailable", "hypervisor_framework", "not_required", "unsupported",
	}, metadata.VirtualizationCapability) {
		return false
	}
	return host.VirtualizationCapability == "" ||
		host.VirtualizationCapability == metadata.VirtualizationCapability
}

func safeReportIdentity(value string, redact func(string) string) bool {
	if strings.TrimSpace(value) == "" || !safeReportText(value, 4096) || redact(value) != value {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "" && parsed.User == nil &&
		!strings.Contains(value, "://")
}

func safeReportText(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, '@') &&
		strings.IndexFunc(value, func(character rune) bool {
			return unicode.IsControl(character) || unicode.Is(unicode.Cf, character)
		}) == -1
}

func validReportSurvey(result SurveyResult, binding Binding) bool {
	if !validReportPassiveState(result) {
		return false
	}
	item := result.Requirements[0]
	return validReportPolicy(result, binding) &&
		sameReportRequirement(item.Requirement, binding.Requirements[0]) &&
		validReportRequirementState(item) && validReportObservation(item)
}

func validReportPassiveState(result SurveyResult) bool {
	return result.ContractVerified && !result.Executed &&
		result.AdapterStatus == "not_applicable" &&
		result.ManagedCapabilities == (SurveyManagedCapabilities{}) &&
		len(result.Requirements) == 1
}

func validReportPolicy(result SurveyResult, binding Binding) bool {
	policyReason := "dependency_recipe_unqualified"
	policy := surveyExternalOnly
	if !slices.Contains(binding.Platforms, result.Host.GOOS+"/"+result.Host.GOARCH) {
		policyReason, policy = surveyPlatformUnsupported, surveyUnsupported
	}
	item := result.Requirements[0]
	return result.PolicyReason == policyReason && item.PolicyReason == policyReason &&
		item.Policy == policy
}

func validReportRequirementState(item RequirementSurvey) bool {
	return !item.Executed && item.Compatibility == surveyUnknown &&
		item.Reachability == surveyNotChecked
}

func sameReportRequirement(actual, expected Requirement) bool {
	return slices.Equal(actual.RequiredFor, expected.RequiredFor) &&
		actual.ToolID == expected.ToolID && actual.Purpose == expected.Purpose && actual.Scope == expected.Scope &&
		actual.ExecutableOption == expected.ExecutableOption && actual.ConfigurationOption == expected.ConfigurationOption &&
		sameReportGuidance(actual, expected)
}

func sameReportGuidance(actual, expected Requirement) bool {
	return actual.ProbeID == expected.ProbeID && actual.GuidanceID == expected.GuidanceID &&
		actual.WhenImageBackend == expected.WhenImageBackend
}

func validReportObservation(item RequirementSurvey) bool {
	if item.Requirement.ConfigurationOption != "" {
		return len(item.Candidates) == 0 && item.Availability == surveyUnknown &&
			item.ReasonCode == surveyPolicyBlocked
	}
	if len(item.Candidates) == 0 {
		return validReportEmptyLookup(item)
	}
	lookup, valid := reportLookupEvidence(item.Candidates)
	if !valid {
		return false
	}
	availability := surveyAvailability(lookup)
	return item.Availability == availability &&
		item.ReasonCode == surveyObservationReason(lookup.ReasonCode, availability)
}

func validReportEmptyLookup(item RequirementSurvey) bool {
	return (item.Availability == surveyUnknown && item.ReasonCode == LookupInvalidPath) ||
		(item.Availability == surveyMissing && item.ReasonCode == LookupMissing)
}

func reportLookupEvidence(candidates []ExecutableCandidate) (LookupResult, bool) {
	lookup := LookupResult{Candidates: slices.Clone(candidates)}
	for i, candidate := range lookup.Candidates {
		if !validReportCandidate(candidate) ||
			!consistentReportOrigin(lookup.Candidates[0].Origin, candidate.Origin) {
			return LookupResult{}, false
		}
		if candidate.State == FileDeferred {
			lookup.Candidates[i].ReasonCode = LookupInspectionDeferred
		}
	}
	explicit := lookup.Candidates[0].Origin == LookupExplicit ||
		lookup.Candidates[0].Origin == LookupExplicitEnv
	lookup.finish(explicit)
	return lookup, true
}

func consistentReportOrigin(first, origin LookupOrigin) bool {
	if first == LookupExplicit || first == LookupExplicitEnv {
		return origin == first
	}
	return origin == LookupPATH || origin == LookupReviewed
}

func validReportCandidate(candidate ExecutableCandidate) bool {
	if candidate.Executed || !slices.Contains([]LookupOrigin{
		LookupExplicit, LookupExplicitEnv, LookupPATH, LookupReviewed,
	}, candidate.Origin) {
		return false
	}
	reason := ""
	switch candidate.State {
	case FileDeferred:
		reason = surveyPolicyBlocked
	case FileExecutable, FileNonregular, FileNonexecutable:
		if candidate.Launcher {
			reason = LookupIndirectLauncher
		}
	case FileMissing, FileBrokenLink, FileInaccessible:
	default:
		return false
	}
	return candidate.ReasonCode == reason
}

func formatReportItem(item RequirementSurvey, metadata PassiveReportMetadata) ReportItem {
	output := ReportItem{
		ID:            item.Requirement.ToolID,
		DisplayName:   reportToolLabel(item.Requirement.ToolID),
		Required:      true,
		Availability:  item.Availability,
		Compatibility: surveyUnknown,
		Reachability:  surveyNotChecked,
		Policy:        item.Policy,
		ReasonCode:    item.ReasonCode,
		Candidates:    []ReportCandidate{},
	}
	suppressed := false
	for _, candidate := range item.Candidates {
		if !safeReportCandidatePaths(candidate, metadata.Redact) {
			suppressed = true
			continue
		}
		output.Candidates = append(
			output.Candidates,
			formatReportCandidate(candidate, item.Requirement),
		)
	}
	return finishReportItem(output, item, suppressed)
}

func finishReportItem(output ReportItem, item RequirementSurvey, suppressed bool) ReportItem {
	if suppressed || item.Availability == surveyAvailable {
		output.Availability, output.ReasonCode = surveyUnknown, surveyPolicyBlocked
	} else if item.Availability != surveyUnknown || len(item.Candidates) > 0 {
		output.ReasonCode = surveyUnavailableReason(item.Availability)
	}
	if !suppressed && item.ReasonCode == LookupIndirectLauncher {
		output.ReasonCode = LookupIndirectLauncher
	}
	if item.Policy == surveyUnsupported {
		output.ReasonCode = surveyPlatformUnsupported
	}
	return output
}

func formatReportCandidate(candidate ExecutableCandidate, requirement Requirement) ReportCandidate {
	availability := surveyFileAvailability(candidate.State)
	reason := surveyUnavailableReason(availability)
	detail := ""
	if candidate.State == FileExecutable {
		availability, reason = surveyUnknown, surveyPolicyBlocked
		detail = "Serialized file identity is unavailable; executable metadata does not establish usable availability."
	}
	source := string(candidate.Origin)
	if candidate.Launcher {
		source, reason = reportIndirectLauncher, LookupIndirectLauncher
	}
	return ReportCandidate{
		Component:     requirement.ToolID,
		SourceKind:    source,
		OptionName:    reportCandidateOption(candidate.Origin, requirement.ExecutableOption),
		LexicalPath:   candidate.LexicalPath,
		ResolvedPath:  candidate.ResolvedPath,
		IsSymlink:     candidate.IsSymlink,
		Availability:  availability,
		Compatibility: surveyUnknown,
		ReasonCode:    reason,
		Detail:        detail,
	}
}

func reportCandidateOption(origin LookupOrigin, option string) string {
	if origin == LookupExplicit || origin == LookupExplicitEnv {
		return option
	}
	return ""
}

func reportToolLabel(tool string) string {
	switch tool {
	case toolDocker:
		return "Docker"
	case reportPodman:
		return "Podman"
	case "apple-container":
		return "Apple Container"
	default:
		return "Kubernetes configuration"
	}
}

func safeReportPath(value string, redact func(string) string) bool {
	if strings.TrimSpace(value) == "" || !safeReportText(value, 32768) || redact(value) != value {
		return false
	}
	// Native absolute paths can contain tools:// as a directory spelling. Parse
	// URI syntax only after recognizing native roots, without cleaning the path.
	if reportNativeRoot(value) {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "" && parsed.User == nil &&
		!strings.Contains(value, "://")
}

func safeReportCandidatePaths(candidate ExecutableCandidate, redact func(string) string) bool {
	return safeReportPath(candidate.LexicalPath, redact) &&
		(candidate.ResolvedPath == "" || safeReportPath(candidate.ResolvedPath, redact))
}

func reportNativeRoot(value string) bool {
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) {
		return true
	}
	if len(value) >= 3 && isReportDriveLetter(value[0]) && value[1] == ':' &&
		strings.ContainsAny(value[2:3], `/\`) {
		return true
	}
	return false
}

func isReportDriveLetter(letter byte) bool {
	return letter >= 'A' && letter <= 'Z' || letter >= 'a' && letter <= 'z'
}
