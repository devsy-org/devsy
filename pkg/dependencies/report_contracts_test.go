package dependencies_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/dependencies"
)

const contractExecutablePath = "/tools/docker"

func passiveContractMetadata() dependencies.PassiveReportMetadata {
	return dependencies.PassiveReportMetadata{
		EvaluatedAt:              time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC),
		VirtualizationCapability: "not_required",
		Redact:                   func(value string) string { return value },
	}
}

func (s *contractsSuite) passiveSurvey(
	bindingID, path string,
) (*dependencies.Catalog, dependencies.SurveyResult) {
	catalog, err := dependencies.LoadCatalog()
	s.Require().NoError(err)
	root := s.T().TempDir()
	environment, err := dependencies.NewLookupEnvironment(map[string]string{"PATH": ""}, root)
	s.Require().NoError(err)
	request := dependencies.SurveyRequest{
		Target: dependencies.SurveyTarget{
			ContextName: "actual-context", ProviderName: "renamed-production-installation",
			ConfigRevision: "config-snapshot-17", ProviderConfigRevision: "provider-snapshot-23",
		},
		Host:        dependencies.SurveyHost{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Environment: environment, Options: map[string]dependencies.SurveyOption{},
	}
	for _, binding := range catalog.Bindings() {
		if binding.ID == bindingID {
			request.Contract = dependencies.ProviderContract{
				Source:            binding.Source,
				ExecutionContract: binding.ExecutionContract,
			}
			request.Options[binding.Requirements[0].ExecutableOption] = dependencies.SurveyOption{
				Value: path, UserProvided: true, Origin: dependencies.LookupExplicitEnv,
			}
		}
	}
	result, err := catalog.Survey(context.Background(), request)
	s.Require().NoError(err)
	return catalog, result
}

func (s *contractsSuite) passiveContract(
	catalog *dependencies.Catalog,
	result dependencies.SurveyResult,
) *reportContract {
	report, err := catalog.FormatPassiveReport(result, passiveContractMetadata())
	s.Require().NoError(err)
	raw, err := json.Marshal(report)
	s.Require().NoError(err)
	decoded, err := s.decodeAndValidateRaw(raw)
	s.Require().NoError(err)
	for _, absent := range []string{
		`"observation"`, `"fileIdentity"`, `"selectedPair"`, `"managedRecipe"`, `"adapterVersion"`,
		`"staleAgainstRevision"`, `"platform"`, `:null`,
	} {
		s.NotContains(string(raw), absent)
	}
	s.Contains(string(raw), `"actions":[]`)
	s.Equal(result.Target.ProviderName, decoded.Target.ProviderName)
	s.Empty(decoded.Actions)
	return decoded
}

func (s *contractsSuite) TestPassiveReportRealSurveyContractsAndPreservation() {
	if runtime.GOOS == osWindows {
		s.T().Skip("Unix sentinel, execute bits, and symlinks; Windows evidence is compile-only")
	}
	root := s.T().TempDir()
	marker := filepath.Join(root, "EXECUTED")
	executable := filepath.Join(root, "docker")
	s.Require().
		NoError(os.WriteFile(executable, []byte("#!/bin/sh\nprintf executed > '"+marker+"'\n"), 0o600))
	info, err := os.Stat(executable)
	s.Require().NoError(err)
	s.Require().NoError(os.Chmod(executable, info.Mode()|0o100))
	plain := filepath.Join(root, "nonexec")
	s.Require().NoError(os.WriteFile(plain, []byte("not executable"), 0o600))
	missing := filepath.Join(root, "missing")
	link := filepath.Join(root, "broken")
	s.Require().NoError(os.Symlink(missing, link))
	dependencytest.Preserve(s.T(), root)
	for _, binding := range []string{providerDocker, providerPodman, providerApple, providerKubernetes} {
		for _, path := range []string{executable, plain, missing, link, "", "https://user:secret@host/tool"} {
			catalog, result := s.passiveSurvey(binding, path)
			report := s.passiveContract(catalog, result)
			s.Equal(binding, report.Target.ProviderType)
			s.Equal("internal:"+binding, report.Target.ProviderSourceID)
			s.NotEqual(availabilityAvailable, report.Availability)
			if binding == providerKubernetes {
				s.Empty(report.Items[0].Candidates)
				s.Equal(availabilityUnknown, report.Availability)
			} else if path == executable {
				s.Require().NotNil(result.Requirements[0].Candidates[0].Identity)
				s.Equal(availabilityUnknown, report.Availability)
			}
		}
	}
	s.NoFileExists(marker)
}

func (s *contractsSuite) TestPassiveReportAllFileStatesPassFrozenValidators() {
	states := []struct {
		state        dependencies.FileState
		availability string
		reason       string
	}{
		{dependencies.FileExecutable, string(availabilityAvailable), string(reasonVersionUnknown)},
		{dependencies.FileMissing, "missing", "dependency_missing"},
		{dependencies.FileBrokenLink, string(availabilityIncomplete), string(reasonIncomplete)},
		{dependencies.FileNonregular, string(availabilityIncomplete), string(reasonIncomplete)},
		{dependencies.FileNonexecutable, string(availabilityIncomplete), string(reasonIncomplete)},
		{dependencies.FileInaccessible, "unknown", string(reasonPolicyBlocked)},
		{dependencies.FileDeferred, "unknown", string(reasonPolicyBlocked)},
	}
	for _, state := range states {
		for _, origin := range []dependencies.LookupOrigin{
			dependencies.LookupExplicit, dependencies.LookupExplicitEnv, dependencies.LookupPATH, dependencies.LookupReviewed,
		} {
			catalog, result := s.passiveSurvey(providerDocker, "")
			item := &result.Requirements[0]
			item.Availability, item.ReasonCode = state.availability, state.reason
			candidate := dependencies.ExecutableCandidate{
				Origin:       origin,
				State:        state.state,
				LexicalPath:  contractExecutablePath,
				ResolvedPath: contractExecutablePath,
			}
			if state.state == dependencies.FileDeferred {
				candidate.ReasonCode = string(reasonPolicyBlocked)
			}
			if origin == dependencies.LookupExplicit || origin == dependencies.LookupExplicitEnv {
				if state.state != dependencies.FileExecutable &&
					state.state != dependencies.FileDeferred {
					item.ReasonCode = "dependency_path_invalid"
				}
			}
			item.Candidates = []dependencies.ExecutableCandidate{candidate}
			s.passiveContract(catalog, result)
		}
	}
}

func (s *contractsSuite) TestPassiveReportGeneratedNegativeContracts() {
	catalog, result := s.passiveSurvey(providerDocker, "")
	item := &result.Requirements[0]
	item.Availability, item.ReasonCode = string(availabilityAvailable), string(reasonVersionUnknown)
	item.Candidates = []dependencies.ExecutableCandidate{{
		Origin: dependencies.LookupPATH, State: dependencies.FileExecutable,
		LexicalPath: contractExecutablePath, ResolvedPath: contractExecutablePath,
	}}
	generated := s.passiveContract(catalog, result)
	mutations := map[string]func(*reportContract){
		"available without identity": func(r *reportContract) {
			r.Items[0].Candidates[0].Availability = availabilityAvailable
		},
		"path identity": func(r *reportContract) {
			c := &r.Items[0].Candidates[0]
			c.Availability = availabilityAvailable
			c.Observation = &candidateFileObservation{FileIdentity: c.LexicalPath}
		},
		"revision identity": func(r *reportContract) {
			c := &r.Items[0].Candidates[0]
			c.Availability = availabilityAvailable
			c.Observation = &candidateFileObservation{FileIdentity: r.Target.ConfigRevision}
		},
		"passive version": func(r *reportContract) {
			c := &r.Items[0].Candidates[0]
			c.Availability = availabilityAvailable
			c.Observation = &candidateFileObservation{
				FileIdentity:    "opaque-fixture-identity",
				ObservedVersion: "1.2.3",
			}
		},
		"compatible": func(r *reportContract) { r.Items[0].Candidates[0].Compatibility = compatibilityCompatible },
		"selection":  func(r *reportContract) { r.Items[0].Candidates[0].Selected = true },
		"managed action": func(r *reportContract) {
			r.Actions = []remediationAction{{
				Kind: actionInstall, Label: "Install", Enabled: true,
				RequiresEffectAuthorization: true, RequiresConfirmation: true,
			}}
		},
	}
	for name, mutate := range mutations {
		s.Run(name, func() {
			changed := cloneReport(s.T(), generated)
			mutate(changed)
			raw, err := json.Marshal(changed)
			s.Require().NoError(err)
			_, err = s.decodeAndValidateRaw(raw)
			s.Require().Error(err)
		})
	}
}

func (s *contractsSuite) TestPassiveReportLauncherSuppressionAndPlatformContracts() {
	catalog, result := s.passiveSurvey(providerApple, "")
	result.Host = dependencies.SurveyHost{GOOS: osLinux, GOARCH: archAMD64}
	result.PolicyReason = string(reasonPlatformUnsupported)
	item := &result.Requirements[0]
	item.PolicyReason, item.Policy = string(reasonPlatformUnsupported), string(policyUnsupported)
	item.Availability, item.ReasonCode = string(
		availabilityAvailable,
	), string(
		reasonIndirectLauncher,
	)
	item.Candidates = []dependencies.ExecutableCandidate{
		{
			Origin:      dependencies.LookupExplicitEnv,
			State:       dependencies.FileExecutable,
			LexicalPath: "/mise/shims/container",
			Launcher:    true,
			ReasonCode:  string(reasonIndirectLauncher),
		},
	}
	report := s.passiveContract(catalog, result)
	s.Equal(reasonPlatformUnsupported, report.ReasonCode)
	s.Equal(availabilityUnknown, report.Availability)
	s.Equal(candidateSourceIndirectLauncher, report.Items[0].Candidates[0].SourceKind)
	s.Equal(reasonIndirectLauncher, report.Items[0].Candidates[0].ReasonCode)
	item.Candidates[0].LexicalPath = "https://user:secret@host/container"
	report = s.passiveContract(catalog, result)
	s.Equal(reasonPlatformUnsupported, report.ReasonCode)
	s.Equal(availabilityUnknown, report.Availability)
	s.Empty(report.Items[0].Candidates)
}

func (s *contractsSuite) TestPassiveReportLauncherAndRedactionContracts() {
	catalog, result := s.passiveSurvey(providerDocker, "")
	item := &result.Requirements[0]
	item.Availability, item.ReasonCode = string(
		availabilityAvailable,
	), string(
		reasonIndirectLauncher,
	)
	item.Candidates = []dependencies.ExecutableCandidate{
		{
			Origin:      dependencies.LookupPATH,
			State:       dependencies.FileExecutable,
			LexicalPath: "/mise/shims/docker",
			Launcher:    true,
			ReasonCode:  string(reasonIndirectLauncher),
		},
	}
	report := s.passiveContract(catalog, result)
	s.Equal(reasonIndirectLauncher, report.ReasonCode)
	s.False(report.Items[0].Candidates[0].Selected)
	metadata := passiveContractMetadata()
	metadata.Redact = func(value string) string {
		if value == "/mise/shims/docker" {
			return ""
		}
		return value
	}
	output, err := catalog.FormatPassiveReport(result, metadata)
	s.Require().NoError(err)
	raw, err := json.Marshal(output)
	s.Require().NoError(err)
	decoded, err := s.decodeAndValidateRaw(raw)
	s.Require().NoError(err)
	s.Equal(reasonPolicyBlocked, decoded.ReasonCode)
	s.Equal(availabilityUnknown, decoded.Availability)
	s.Empty(decoded.Items[0].Candidates)
}
