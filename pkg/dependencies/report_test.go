package dependencies

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const (
	reportTestPath       = "/tools/docker"
	reportTestIgnored    = "ignored-private-fragment"
	reportTestOther      = "other"
	reportTestUnreviewed = "unreviewed secret"
	reportTestIncomplete = "dependency_incomplete"
)

type reportSuite struct {
	suite.Suite
	catalog *Catalog
}

func TestPassiveReport(t *testing.T) { suite.Run(t, new(reportSuite)) }

func (s *reportSuite) SetupTest() {
	var err error
	s.catalog, err = LoadCatalog()
	s.Require().NoError(err)
}

func reportMetadata() PassiveReportMetadata {
	return PassiveReportMetadata{
		EvaluatedAt: time.Date(
			2026,
			time.October,
			19,
			12,
			30,
			40,
			123,
			time.FixedZone("offset", -21600),
		),
		VirtualizationCapability: "not_required",
		Redact:                   func(value string) string { return value },
	}
}

func (s *reportSuite) TestBindingsAndExplicitMetadata() {
	for _, binding := range []string{"docker", reportPodman, "apple", reportKubernetes} {
		result := s.result(binding)
		report := s.format(result, reportMetadata())
		s.Equal(binding, report.Target.ProviderType)
		s.Equal("internal:"+binding, report.Target.ProviderSourceID)
		s.Equal(result.Target.ProviderName, report.Target.ProviderName)
		s.Equal(result.Target.ContextName, report.Target.ContextName)
		s.Equal(result.Target.ConfigRevision, report.Target.ConfigRevision)
		s.Equal(result.Target.ProviderConfigRevision, report.Target.ProviderConfigRevision)
		s.Equal("2026-10-19T18:30:40.000000123Z", report.Target.EvaluatedAt)
		s.Equal("not_required", report.Host.VirtualizationCapability)
		s.Empty(report.Actions)
		s.NotNil(report.Actions)
		s.NotNil(report.Items[0].Candidates)
	}
}

func (s *reportSuite) TestHostFactsAreExplicitAndPreserved() {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			for _, capability := range []string{
				"kvm_available", "kvm_unavailable", "hypervisor_framework", "not_required", "unsupported",
			} {
				result, metadata := s.result(toolDocker), reportMetadata()
				result.Host = SurveyHost{
					GOOS:                     goos,
					GOARCH:                   goarch,
					VirtualizationCapability: capability,
				}
				metadata.VirtualizationCapability, metadata.OSVersion = capability, "15.2 (24C101)"
				report := s.format(result, metadata)
				s.Equal(goos, report.Host.GOOS)
				s.Equal(goarch, report.Host.GOARCH)
				s.Equal(capability, report.Host.VirtualizationCapability)
				s.Equal(metadata.OSVersion, report.Host.OSVersion)
			}
		}
	}
}

func (s *reportSuite) TestEveryFileStateAndOrigin() {
	for _, test := range []struct {
		state        FileState
		availability string
		reason       string
	}{
		{FileExecutable, surveyUnknown, surveyPolicyBlocked},
		{FileMissing, surveyMissing, LookupMissing},
		{FileBrokenLink, surveyIncomplete, reportTestIncomplete},
		{FileNonregular, surveyIncomplete, reportTestIncomplete},
		{FileNonexecutable, surveyIncomplete, reportTestIncomplete},
		{FileInaccessible, surveyUnknown, surveyPolicyBlocked},
		{FileDeferred, surveyUnknown, surveyPolicyBlocked},
	} {
		for _, origin := range []LookupOrigin{LookupExplicit, LookupExplicitEnv, LookupPATH, LookupReviewed} {
			result := s.result("docker")
			candidate := ExecutableCandidate{
				Origin:      origin,
				LexicalPath: reportTestPath,
				State:       test.state,
			}
			if test.state == FileDeferred {
				candidate.ReasonCode = LookupInspectionDeferred
			}
			s.observe(&result, candidate)
			report := s.format(result, reportMetadata())
			output := report.Items[0].Candidates[0]
			s.Equal(string(origin), output.SourceKind)
			s.Equal(reportCandidateOption(origin, "DOCKER_PATH"), output.OptionName)
			if test.state == FileExecutable {
				s.Contains(output.Detail, "Serialized file identity is unavailable")
			}
			s.Equal(test.availability, output.Availability)
			s.Equal(test.availability, report.Availability)
			s.Equal(test.reason, output.ReasonCode)
			s.Equal(test.reason, report.ReasonCode)
			s.False(output.Selected)
			s.False(output.Executed)
		}
	}
}

func (s *reportSuite) TestLauncherAndUnsupportedPolicy() {
	result := s.result("apple")
	result.Host = SurveyHost{GOOS: "linux", GOARCH: "amd64"}
	result.PolicyReason = surveyPlatformUnsupported
	result.Requirements[0].PolicyReason = surveyPlatformUnsupported
	result.Requirements[0].Policy = surveyUnsupported
	s.observe(&result, ExecutableCandidate{
		Origin:       LookupExplicitEnv,
		LexicalPath:  "/mise/shims/container",
		ResolvedPath: "/mise/shims/container",
		State:        FileExecutable,
		Launcher:     true,
		ReasonCode:   LookupIndirectLauncher,
	})
	report := s.format(result, reportMetadata())
	s.Equal(surveyUnknown, report.Availability)
	s.Equal(surveyUnsupported, report.Policy)
	s.Equal(surveyPlatformUnsupported, report.ReasonCode)
	s.Equal(surveyPlatformUnsupported, report.Items[0].ReasonCode)
	s.Equal(reportIndirectLauncher, report.Items[0].Candidates[0].SourceKind)
	s.Equal(LookupIndirectLauncher, report.Items[0].Candidates[0].ReasonCode)
}

func (s *reportSuite) TestSuppressionIsUnknownAndNeverPartialPath() {
	for _, path := range []string{
		"https://user:private-fragment@host/docker", "ssh://host/docker", "file://host/docker",
		`C:\user:private-fragment@host\docker.exe`, "/tools/line\nfeed", "/tools/\u202edocker", "/private-fragment/docker",
	} {
		for _, resolved := range []bool{false, true} {
			result := s.result("docker")
			candidate := ExecutableCandidate{
				Origin:      LookupPATH,
				State:       FileMissing,
				LexicalPath: path,
			}
			if resolved {
				candidate.LexicalPath, candidate.ResolvedPath = "/safe/docker", path
			}
			s.observe(&result, candidate)
			metadata := reportMetadata()
			metadata.Redact = func(value string) string { return strings.ReplaceAll(value, "private-fragment", "REDACTED") }
			report := s.format(result, metadata)
			s.Equal(surveyUnknown, report.Availability)
			s.Equal(surveyPolicyBlocked, report.ReasonCode)
			s.Empty(report.Items[0].Candidates)
			encoded, err := json.Marshal(report)
			s.Require().NoError(err)
			s.NotContains(string(encoded), "private-fragment")
			s.NotContains(string(encoded), "REDACTED")
		}
	}
}

func (s *reportSuite) TestNativeSpellingAndRefusingPathPolicy() {
	for _, path := range []string{"/tmp/tools://docker", `C:\tools\docker.exe`, `\\server\tools\docker.exe`} {
		result := s.result("docker")
		s.observe(&result, ExecutableCandidate{
			Origin:       LookupReviewed,
			State:        FileExecutable,
			LexicalPath:  path,
			ResolvedPath: path,
			IsSymlink:    true,
		})
		report := s.format(result, reportMetadata())
		s.Equal(path, report.Items[0].Candidates[0].LexicalPath)
		s.Equal(path, report.Items[0].Candidates[0].ResolvedPath)
		s.True(report.Items[0].Candidates[0].IsSymlink)
		metadata := reportMetadata()
		metadata.Redact = func(value string) string {
			if value == path {
				return ""
			}
			return value
		}
		report = s.format(result, metadata)
		s.Equal(surveyUnknown, report.Availability)
		s.Empty(report.Items[0].Candidates)
	}
}

func (s *reportSuite) TestInvalidExplicitWithoutCandidates() {
	result := s.result("docker")
	result.Requirements[0].Availability = surveyUnknown
	result.Requirements[0].ReasonCode = LookupInvalidPath
	report := s.format(result, reportMetadata())
	s.Equal(LookupInvalidPath, report.ReasonCode)
	s.Empty(report.Items[0].Candidates)
}

func (s *reportSuite) TestNoSliceAliasingOrAmbientEvidence() {
	result := s.result("docker")
	result.Target.StoreID = reportTestIgnored
	result.Target.ProviderFingerprint = reportTestIgnored
	result.Host.NativeArchitecture = reportTestIgnored
	s.observe(&result, ExecutableCandidate{
		Origin:       LookupPATH,
		State:        FileExecutable,
		LexicalPath:  reportTestPath,
		ResolvedPath: reportTestPath,
		Platform:     reportTestIgnored,
		Identity:     &FileObservation{},
	})
	first := s.format(result, reportMetadata())
	second := s.format(result, reportMetadata())
	first.Items[0].Candidates[0].LexicalPath = "mutated"
	first.Items[0].ID = "mutated"
	s.Equal(reportTestPath, result.Requirements[0].Candidates[0].LexicalPath)
	s.Equal(reportTestPath, second.Items[0].Candidates[0].LexicalPath)
	s.Equal("docker", result.Requirements[0].Requirement.ToolID)
	encoded, err := json.Marshal(second)
	s.Require().NoError(err)
	for _, absent := range []string{
		"observation", "fileIdentity", "observedVersion", "selectedPair", "managedRecipe", "adapterVersion",
		"staleAgainstRevision", reportTestIgnored, `"platform"`, `:null`,
	} {
		s.NotContains(string(encoded), absent)
	}
}

func (s *reportSuite) result(bindingID string) SurveyResult {
	var binding Binding
	for _, candidate := range s.catalog.Bindings() {
		if candidate.ID == bindingID {
			binding = candidate
		}
	}
	s.Require().NotEmpty(binding.ID)
	result := SurveyResult{
		Target: SurveyTarget{
			ContextName: "Work context", ProviderName: "Renamed installation",
			ConfigRevision: "snapshot-one", ProviderConfigRevision: "provider-two",
		},
		Host:             SurveyHost{GOOS: "darwin", GOARCH: "arm64"},
		CatalogID:        s.catalog.ID(),
		CatalogHash:      s.catalog.Hash(),
		BindingID:        binding.ID,
		ContractVerified: true,
		AdapterStatus:    "not_applicable",
		PolicyReason:     "dependency_recipe_unqualified",
		Requirements: []RequirementSurvey{
			{
				Requirement:   binding.Requirements[0],
				Availability:  surveyMissing,
				Compatibility: surveyUnknown,
				Reachability:  surveyNotChecked,
				Policy:        surveyExternalOnly,
				PolicyReason:  "dependency_recipe_unqualified",
				ReasonCode:    LookupMissing,
			},
		},
	}
	if bindingID == reportKubernetes {
		result.Requirements[0].Availability = surveyUnknown
		result.Requirements[0].ReasonCode = surveyPolicyBlocked
	}
	return result
}

func (s *reportSuite) observe(result *SurveyResult, candidates ...ExecutableCandidate) {
	item := &result.Requirements[0]
	item.Candidates = candidates
	lookup := LookupResult{Candidates: candidates}
	lookup.finish(len(candidates) > 0 &&
		(candidates[0].Origin == LookupExplicit || candidates[0].Origin == LookupExplicitEnv))
	item.Availability = surveyAvailability(lookup)
	item.ReasonCode = surveyObservationReason(lookup.ReasonCode, item.Availability)
	for i := range item.Candidates {
		if item.Candidates[i].ReasonCode == LookupInspectionDeferred {
			item.Candidates[i].ReasonCode = surveyPolicyBlocked
		}
	}
}

func (s *reportSuite) format(result SurveyResult, metadata PassiveReportMetadata) Report {
	report, err := s.catalog.FormatPassiveReport(result, metadata)
	s.Require().NoError(err)
	return report
}
