package dependencies

import (
	"strings"
	"time"
)

func (s *reportSuite) TestForgedSurveyInputsRejected() {
	mutations := map[string]func(*SurveyResult){
		"catalog ID":            func(r *SurveyResult) { r.CatalogID = surveyUnknown },
		"catalog hash":          func(r *SurveyResult) { r.CatalogHash = surveyUnknown },
		"unknown binding":       func(r *SurveyResult) { r.BindingID = "custom" },
		"external binding":      func(r *SurveyResult) { r.BindingID = "microsandbox" },
		"unverified":            func(r *SurveyResult) { r.ContractVerified = false },
		"adapter":               func(r *SurveyResult) { r.AdapterStatus = "forged_adapter_qualification" },
		"executed":              func(r *SurveyResult) { r.Executed = true },
		"managed install":       func(r *SurveyResult) { r.ManagedCapabilities.Install = true },
		"managed select":        func(r *SurveyResult) { r.ManagedCapabilities.Select = true },
		"managed remove":        func(r *SurveyResult) { r.ManagedCapabilities.Remove = true },
		"managed update":        func(r *SurveyResult) { r.ManagedCapabilities.Update = true },
		"missing requirement":   func(r *SurveyResult) { r.Requirements = nil },
		"duplicate requirement": func(r *SurveyResult) { r.Requirements = append(r.Requirements, r.Requirements[0]) },
		"tool":                  func(r *SurveyResult) { r.Requirements[0].Requirement.ToolID = reportPodman },
		"purpose":               func(r *SurveyResult) { r.Requirements[0].Requirement.Purpose = reportTestOther },
		"scope":                 func(r *SurveyResult) { r.Requirements[0].Requirement.Scope = "guest" },
		"required for":          func(r *SurveyResult) { r.Requirements[0].Requirement.RequiredFor[0] = reportTestOther },
		"executable option":     func(r *SurveyResult) { r.Requirements[0].Requirement.ExecutableOption = "OTHER" },
		"configuration option":  func(r *SurveyResult) { r.Requirements[0].Requirement.ConfigurationOption = "OTHER" },
		"probe ID":              func(r *SurveyResult) { r.Requirements[0].Requirement.ProbeID = reportTestOther },
		"guidance":              func(r *SurveyResult) { r.Requirements[0].Requirement.GuidanceID = reportTestOther },
		"backend":               func(r *SurveyResult) { r.Requirements[0].Requirement.WhenImageBackend = reportTestOther },
		"policy reason":         func(r *SurveyResult) { r.PolicyReason = reportTestUnreviewed },
		"item policy reason":    func(r *SurveyResult) { r.Requirements[0].PolicyReason = reportTestUnreviewed },
		"item policy":           func(r *SurveyResult) { r.Requirements[0].Policy = "managed_selected" },
		"item executed":         func(r *SurveyResult) { r.Requirements[0].Executed = true },
		"compatible":            func(r *SurveyResult) { r.Requirements[0].Compatibility = "compatible" },
		"incompatible":          func(r *SurveyResult) { r.Requirements[0].Compatibility = "incompatible" },
		"reachability":          func(r *SurveyResult) { r.Requirements[0].Reachability = "reachable" },
		"availability":          func(r *SurveyResult) { r.Requirements[0].Availability = "available" },
		"reason":                func(r *SurveyResult) { r.Requirements[0].ReasonCode = reportTestUnreviewed },
	}
	for name, mutate := range mutations {
		s.Run(name, func() {
			result := s.result("docker")
			mutate(&result)
			s.reject(result, reportMetadata())
		})
	}
	for _, catalog := range []*Catalog{nil, {}, {hash: "fabricated"}} {
		report, err := catalog.FormatPassiveReport(s.result("docker"), reportMetadata())
		s.EqualError(err, reportInvalid)
		s.Equal(Report{}, report)
	}
}

func (s *reportSuite) reject(result SurveyResult, metadata PassiveReportMetadata) {
	report, err := s.catalog.FormatPassiveReport(result, metadata)
	s.EqualError(err, reportInvalid)
	s.Equal(Report{}, report)
}

func (s *reportSuite) TestMalformedCandidateInputsRejected() {
	for name, mutate := range map[string]func(*ExecutableCandidate){
		"executed":         func(c *ExecutableCandidate) { c.Executed = true },
		"unknown state":    func(c *ExecutableCandidate) { c.State = surveyUnknown },
		"unknown origin":   func(c *ExecutableCandidate) { c.Origin = "managed_store" },
		"arbitrary reason": func(c *ExecutableCandidate) { c.ReasonCode = "private-fragment" },
		"version reason":   func(c *ExecutableCandidate) { c.ReasonCode = surveyVersionUnknown },
	} {
		s.Run(name, func() {
			result := s.result("docker")
			candidate := ExecutableCandidate{
				Origin:      LookupPATH,
				State:       FileMissing,
				LexicalPath: reportTestPath,
			}
			mutate(&candidate)
			result.Requirements[0].Candidates = []ExecutableCandidate{candidate}
			s.reject(result, reportMetadata())
		})
	}
	result := s.result("docker")
	result.Requirements[0].Candidates = []ExecutableCandidate{
		{Origin: LookupExplicit, State: FileMissing, LexicalPath: reportTestPath},
		{Origin: LookupPATH, State: FileMissing, LexicalPath: "/fallback/docker"},
	}
	s.reject(result, reportMetadata())
	result = s.result(reportKubernetes)
	result.Requirements[0].Candidates = []ExecutableCandidate{
		{Origin: LookupPATH, State: FileMissing},
	}
	s.reject(result, reportMetadata())
}

func (s *reportSuite) TestRequiredMetadataAndHostFactsRejected() {
	for name, mutate := range map[string]func(*SurveyResult){
		"context identity":           func(r *SurveyResult) { r.Target.ContextName = "" },
		"provider identity":          func(r *SurveyResult) { r.Target.ProviderName = "" },
		"config revision":            func(r *SurveyResult) { r.Target.ConfigRevision = "" },
		"provider revision":          func(r *SurveyResult) { r.Target.ProviderConfigRevision = "" },
		"empty OS":                   func(r *SurveyResult) { r.Host.GOOS = "" },
		"unknown OS":                 func(r *SurveyResult) { r.Host.GOOS = "freebsd" },
		"empty architecture":         func(r *SurveyResult) { r.Host.GOARCH = "" },
		"unknown architecture":       func(r *SurveyResult) { r.Host.GOARCH = "386" },
		"conflicting virtualization": func(r *SurveyResult) { r.Host.VirtualizationCapability = "kvm_available" },
	} {
		s.Run(name, func() {
			result := s.result("docker")
			mutate(&result)
			s.reject(result, reportMetadata())
		})
	}
	for name, mutate := range map[string]func(*PassiveReportMetadata){
		"time": func(m *PassiveReportMetadata) { m.EvaluatedAt = time.Time{} },
		"invalid RFC3339 year": func(m *PassiveReportMetadata) {
			m.EvaluatedAt = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
		},
		"redactor":               func(m *PassiveReportMetadata) { m.Redact = nil },
		"virtualization":         func(m *PassiveReportMetadata) { m.VirtualizationCapability = "" },
		"unknown virtualization": func(m *PassiveReportMetadata) { m.VirtualizationCapability = surveyUnknown },
		"unsafe OS version":      func(m *PassiveReportMetadata) { m.OSVersion = "secret\nversion" },
	} {
		s.Run(name, func() {
			metadata := reportMetadata()
			mutate(&metadata)
			s.reject(s.result("docker"), metadata)
		})
	}
}

func (s *reportSuite) TestUnsafeIdentitiesAreRejectedWithoutRepair() {
	for _, value := range []string{
		" ", "name\nsecret", "https://host/provider", "file:/provider", "user:password@host", strings.Repeat("x", 4097),
		"private-fragment", "provider\u202e", string([]byte{0xff}),
	} {
		for _, field := range []string{"context", "provider", "config", "providerConfig", "osVersion"} {
			result, metadata := s.result("docker"), reportMetadata()
			metadata.Redact = func(value string) string { return strings.ReplaceAll(value, "private-fragment", "") }
			switch field {
			case "context":
				result.Target.ContextName = value
			case "provider":
				result.Target.ProviderName = value
			case "config":
				result.Target.ConfigRevision = value
			case "providerConfig":
				result.Target.ProviderConfigRevision = value
			case "osVersion":
				metadata.OSVersion = value
			}
			s.reject(result, metadata)
		}
	}
}
