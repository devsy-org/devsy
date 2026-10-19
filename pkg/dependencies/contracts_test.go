package dependencies_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/blang/semver/v4"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/suite"
)

const (
	reportSchemaID          = "devsy.dependencies.report.v1"
	reportSchemaVersion     = 1
	flatArchiveLayoutPolicy = "flat_verbatim_no_links"

	keywordDefs       = "$defs"
	keywordRef        = "$ref"
	keywordType       = "type"
	keywordConst      = "const"
	keywordEnum       = "enum"
	keywordMinLength  = "minLength"
	keywordMinItems   = "minItems"
	keywordProperties = "properties"
	keywordItems      = "items"

	schemaTypeObject  = "object"
	schemaTypeArray   = "array"
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeBoolean = "boolean"

	canonicalMicroSandboxRepo = "github.com/devsy-org/devsy-provider-microsandbox"
	canonicalLimaRepo         = "github.com/devsy-org/devsy-provider-lima"
	canonicalOrbStackRepo     = "github.com/devsy-org/devsy-provider-orbstack"

	providerMicroSandbox = "microsandbox"
	providerDocker       = "docker"
	providerPodman       = "podman"
	providerApple        = "apple"
	providerKubernetes   = "kubernetes"
	providerSSH          = "ssh"
	providerTypeExternal = "external"
	contextKeyProvider   = "provider"

	osLinux   = "linux"
	osDarwin  = "darwin"
	osWindows = "windows"

	archAMD64 = "amd64"
	archARM64 = "arm64"

	capKVMAvailable        = "kvm_available"
	capKVMUnavailable      = "kvm_unavailable"
	capHypervisorFramework = "hypervisor_framework"
	capNotRequired         = "not_required"
	capUnsupported         = "unsupported"

	qualNotRun    = "not_run"
	qualBlocked   = "blocked"
	qualQualified = "qualified"

	gateG01 = "G01"
	gateG02 = "G02"
	gateG03 = "G03"
	gateG04 = "G04"
	gateG05 = "G05"
	gateG06 = "G06"
	gateG07 = "G07"
	gateG08 = "G08"
	gateG09 = "G09"

	fixtureMissing         = "report_missing.json"
	fixtureExternal        = "report_external.json"
	fixtureIncompletePair  = "report_incomplete_pair.json"
	fixtureManagedDisabled = "report_managed_disabled_candidate.json"
	fixtureUnsupportedHost = "report_unsupported_host.json"
	fixtureStoppedRuntime  = "report_stopped_runtime.json"
	fixtureStaleTarget     = "report_stale_target.json"
	fixtureUnknownCustom   = "report_unknown_custom_source.json"
	fixtureConfigConflict  = "report_configuration_conflict.json"
	fixtureOfflineMissing  = "report_offline_asset_missing.json"
)

type surveyMode string

const (
	surveyModePassive     surveyMode = "passive"
	surveyModeActiveProbe surveyMode = "active_probe"
)

type availabilityState string

const (
	availabilityAvailable  availabilityState = "available"
	availabilityMissing    availabilityState = "missing"
	availabilityIncomplete availabilityState = "incomplete"
	availabilityUnknown    availabilityState = "unknown"
)

type compatibilityState string

const (
	compatibilityCompatible   compatibilityState = "compatible"
	compatibilityIncompatible compatibilityState = "incompatible"
	compatibilityUnknown      compatibilityState = "unknown"
)

type reachabilityState string

const (
	reachabilityReachable     reachabilityState = "reachable"
	reachabilityUnreachable   reachabilityState = "unreachable"
	reachabilityNotChecked    reachabilityState = "not_checked"
	reachabilityNotApplicable reachabilityState = "not_applicable"
)

type policyState string

const (
	policyExternalOnly          policyState = "external_only"
	policyManagedAllowed        policyState = "managed_allowed"
	policyManagedSelected       policyState = "managed_selected"
	policyUnmanagedCustomSource policyState = "unmanaged_custom_source"
	policyUnsupported           policyState = "unsupported"
)

type reasonCode string

const (
	reasonReady                 reasonCode = "dependency_ready"
	reasonMissing               reasonCode = "dependency_missing"
	reasonIncomplete            reasonCode = "dependency_incomplete"
	reasonPathInvalid           reasonCode = "dependency_path_invalid"
	reasonVersionIncompatible   reasonCode = "dependency_version_incompatible"
	reasonVersionUnknown        reasonCode = "dependency_version_unknown"
	reasonPlatformUnsupported   reasonCode = "dependency_platform_unsupported"
	reasonPolicyBlocked         reasonCode = "dependency_policy_blocked"
	reasonRuntimeUnreachable    reasonCode = "dependency_runtime_unreachable"
	reasonAuthRequired          reasonCode = "dependency_auth_required"
	reasonProbeTimeout          reasonCode = "dependency_probe_timeout"
	reasonIntegrityFailed       reasonCode = "dependency_integrity_failed"
	reasonStoreUnwritable       reasonCode = "dependency_store_unwritable"
	reasonRecipeUnqualified     reasonCode = "dependency_recipe_unqualified"
	reasonSelectionStale        reasonCode = "dependency_selection_stale"
	reasonInUse                 reasonCode = "dependency_in_use"
	reasonOperationInterrupted  reasonCode = "dependency_operation_interrupted"
	reasonConfigurationConflict reasonCode = "dependency_configuration_conflict"
	reasonOfflineAssetMissing   reasonCode = "dependency_offline_asset_missing"
	reasonIndirectLauncher      reasonCode = "dependency_indirect_launcher"
)

type providerSourceKind string

const (
	providerSourceBuiltinInternal          providerSourceKind = "builtin_internal"
	providerSourceCanonicalExternalRelease providerSourceKind = "canonical_external_release"
	providerSourceCustomGitHub             providerSourceKind = "custom_github"
	providerSourceCustomLocal              providerSourceKind = "custom_local"
	providerSourceCustomURL                providerSourceKind = "custom_url"
)

type candidateSourceKind string

const (
	candidateSourceExplicitOption   candidateSourceKind = "explicit_option"
	candidateSourceExplicitEnv      candidateSourceKind = "explicit_env"
	candidateSourceManagedStore     candidateSourceKind = "managed_store"
	candidateSourceExternalPath     candidateSourceKind = "external_path"
	candidateSourceWellKnownPath    candidateSourceKind = "well_known_path"
	candidateSourceAppBundle        candidateSourceKind = "app_bundle"
	candidateSourceIndirectLauncher candidateSourceKind = "indirect_launcher"
)

type actionKind string

const (
	actionInstall  actionKind = "install"
	actionRepair   actionKind = "repair"
	actionSelect   actionKind = "select"
	actionRemove   actionKind = "remove"
	actionOpenDocs actionKind = "open_docs"
)

type targetIdentity struct {
	ContextName            string             `json:"contextName"`
	ProviderName           string             `json:"providerName"`
	ProviderType           string             `json:"providerType"`
	ProviderSourceID       string             `json:"providerSourceId"`
	ProviderSourceKind     providerSourceKind `json:"providerSourceKind"`
	AdapterVersion         string             `json:"adapterVersion,omitempty"`
	TrustedBuiltinIdentity bool               `json:"trustedBuiltinIdentity"`
	ConfigRevision         string             `json:"configRevision"`
	ProviderConfigRevision string             `json:"providerConfigRevision"`
	StaleAgainstRevision   string             `json:"staleAgainstRevision,omitempty"`
	EvaluatedAt            string             `json:"evaluatedAt"`
}

type hostIdentity struct {
	GOOS                     string `json:"goos"`
	GOARCH                   string `json:"goarch"`
	OSVersion                string `json:"osVersion,omitempty"`
	VirtualizationCapability string `json:"virtualizationCapability"`
}

type managedRecipeStatus struct {
	RecipeID               string   `json:"recipeId"`
	Tuple                  string   `json:"tuple"`
	CandidateVersion       string   `json:"candidateVersion,omitempty"`
	IllustrativeBundleRoot string   `json:"illustrativeBundleRoot,omitempty"`
	Enabled                bool     `json:"enabled"`
	QualificationState     string   `json:"qualificationState"`
	BlockingGates          []string `json:"blockingGates"`
	ArchiveLayout          string   `json:"archiveLayout"`
	ExpectedFiles          []string `json:"expectedFiles"`
}

type selectedMicroSandboxPair struct {
	MSBLexicalPath        string              `json:"msbLexicalPath"`
	MSBResolvedPath       string              `json:"msbResolvedPath"`
	LibkrunfwLexicalPath  string              `json:"libkrunfwLexicalPath"`
	LibkrunfwResolvedPath string              `json:"libkrunfwResolvedPath"`
	SourceKind            candidateSourceKind `json:"sourceKind"`
}

type candidateFileObservation struct {
	FileIdentity    string `json:"fileIdentity"`
	Platform        string `json:"platform,omitempty"`
	ObservedVersion string `json:"observedVersion,omitempty"`
}

type candidateObservation struct {
	Component     string                    `json:"component"`
	SourceKind    candidateSourceKind       `json:"sourceKind"`
	OptionName    string                    `json:"optionName,omitempty"`
	LexicalPath   string                    `json:"lexicalPath"`
	ResolvedPath  string                    `json:"resolvedPath"`
	IsSymlink     bool                      `json:"isSymlink,omitempty"`
	Observation   *candidateFileObservation `json:"observation,omitempty"`
	Executed      bool                      `json:"executed"`
	Selected      bool                      `json:"selected"`
	Availability  availabilityState         `json:"availability"`
	Compatibility compatibilityState        `json:"compatibility"`
	ReasonCode    reasonCode                `json:"reasonCode"`
	Detail        string                    `json:"detail,omitempty"`
}

type dependencyItemReport struct {
	ID            string                    `json:"id"`
	DisplayName   string                    `json:"displayName"`
	Required      bool                      `json:"required"`
	Executed      bool                      `json:"executed"`
	Availability  availabilityState         `json:"availability"`
	Compatibility compatibilityState        `json:"compatibility"`
	Reachability  reachabilityState         `json:"reachability"`
	Policy        policyState               `json:"policy"`
	ReasonCode    reasonCode                `json:"reasonCode"`
	SelectedPair  *selectedMicroSandboxPair `json:"selectedPair,omitempty"`
	Candidates    []candidateObservation    `json:"candidates"`
}

type remediationAction struct {
	Kind                        actionKind `json:"kind"`
	Label                       string     `json:"label"`
	Enabled                     bool       `json:"enabled"`
	DisabledReasonCode          reasonCode `json:"disabledReasonCode,omitempty"`
	RequiresEffectAuthorization bool       `json:"requiresEffectAuthorization"`
	RequiresConfirmation        bool       `json:"requiresConfirmation"`
	CLICommand                  string     `json:"cliCommand,omitempty"`
}

type reportContract struct {
	Schema        string                 `json:"schema"`
	SchemaVersion int                    `json:"schemaVersion"`
	SurveyMode    surveyMode             `json:"surveyMode"`
	Executed      bool                   `json:"executed"`
	Target        targetIdentity         `json:"target"`
	Host          hostIdentity           `json:"host"`
	Availability  availabilityState      `json:"availability"`
	Compatibility compatibilityState     `json:"compatibility"`
	Reachability  reachabilityState      `json:"reachability"`
	Policy        policyState            `json:"policy"`
	ReasonCode    reasonCode             `json:"reasonCode"`
	Message       string                 `json:"message"`
	Hint          string                 `json:"hint,omitempty"`
	ManagedRecipe *managedRecipeStatus   `json:"managedRecipe,omitempty"`
	Items         []dependencyItemReport `json:"items"`
	Actions       []remediationAction    `json:"actions"`
}

type canonicalAdapterBaseline struct {
	maxPublishedVersion          semver.Version
	supportsPathPairRuntimeCheck bool
}

type canonicalSourceSpec struct {
	repo string
	tag  string
	ver  semver.Version
}

type targetContext struct {
	report             *reportContract
	isCanonicalMSB     bool
	adapterSupportsT08 bool
}

type dimensionSet struct {
	mode     surveyMode
	executed bool
	avail    availabilityState
	compat   compatibilityState
	reach    reachabilityState
	policy   policyState
	reason   reasonCode
}

type jsonSchemaNode struct {
	Schema               string                    `json:"$schema"`
	ID                   string                    `json:"$id"`
	Title                string                    `json:"title"`
	Description          string                    `json:"description"`
	Type                 string                    `json:"type"`
	Ref                  string                    `json:"$ref"`
	Const                any                       `json:"const"`
	Enum                 []string                  `json:"enum"`
	MinLength            int                       `json:"minLength"`
	MinItems             int                       `json:"minItems"`
	AdditionalProperties *bool                     `json:"additionalProperties"`
	Required             []string                  `json:"required"`
	Properties           map[string]jsonSchemaNode `json:"properties"`
	Items                *jsonSchemaNode           `json:"items"`
	Defs                 map[string]jsonSchemaNode `json:"$defs"`
}

type rawShapeCase struct {
	name   string
	base   []byte
	mutate func(doc map[string]any)
	errSub string
}

type nestedCandidateMutationCase struct {
	name            string
	mutate          func(r *reportContract)
	errSub          string
	expectSchemaErr bool
}

type schemaKeywordMutationCase struct {
	name   string
	mutate func(doc map[string]any)
	errSub string
}

type schemaLiteralCase struct {
	name   string
	schema string
	input  string
	errSub string
}

type contractsSuite struct {
	suite.Suite
	contractsDir string
	schema       *jsonSchemaNode
}

var (
	allowedSchemaNodeKeywords = []string{
		"$schema",
		"$id",
		"title",
		"description",
		keywordDefs,
		keywordRef,
		keywordType,
		keywordConst,
		keywordEnum,
		keywordMinLength,
		keywordMinItems,
		"additionalProperties",
		"required",
		keywordProperties,
		keywordItems,
	}
	schemaMetadataKeywords = []string{"$schema", "$id", "title", "description"}
	// schemaKeywordsByType lists the only validation keywords validateSchemaValue
	// evaluates for each type; anything else would be accepted but ignored.
	schemaKeywordsByType = map[string][]string{
		schemaTypeObject:  {"additionalProperties", "required", keywordProperties},
		schemaTypeArray:   {keywordMinItems, keywordItems},
		schemaTypeString:  {keywordConst, keywordEnum, keywordMinLength},
		schemaTypeInteger: {keywordConst},
		schemaTypeBoolean: {},
	}
	allSurveyModes = []surveyMode{
		surveyModePassive,
		surveyModeActiveProbe,
	}
	allAvailabilityStates = []availabilityState{
		availabilityAvailable,
		availabilityMissing,
		availabilityIncomplete,
		availabilityUnknown,
	}
	allCompatibilityStates = []compatibilityState{
		compatibilityCompatible,
		compatibilityIncompatible,
		compatibilityUnknown,
	}
	allReachabilityStates = []reachabilityState{
		reachabilityReachable,
		reachabilityUnreachable,
		reachabilityNotChecked,
		reachabilityNotApplicable,
	}
	allPolicyStates = []policyState{
		policyExternalOnly,
		policyManagedAllowed,
		policyManagedSelected,
		policyUnmanagedCustomSource,
		policyUnsupported,
	}
	allReasonCodes = []reasonCode{
		reasonReady,
		reasonMissing,
		reasonIncomplete,
		reasonPathInvalid,
		reasonVersionIncompatible,
		reasonVersionUnknown,
		reasonPlatformUnsupported,
		reasonPolicyBlocked,
		reasonRuntimeUnreachable,
		reasonAuthRequired,
		reasonProbeTimeout,
		reasonIntegrityFailed,
		reasonStoreUnwritable,
		reasonRecipeUnqualified,
		reasonSelectionStale,
		reasonInUse,
		reasonOperationInterrupted,
		reasonConfigurationConflict,
		reasonOfflineAssetMissing,
		reasonIndirectLauncher,
	}
	allProviderSourceKinds = []providerSourceKind{
		providerSourceBuiltinInternal,
		providerSourceCanonicalExternalRelease,
		providerSourceCustomGitHub,
		providerSourceCustomLocal,
		providerSourceCustomURL,
	}
	allCandidateSourceKinds = []candidateSourceKind{
		candidateSourceExplicitOption,
		candidateSourceExplicitEnv,
		candidateSourceManagedStore,
		candidateSourceExternalPath,
		candidateSourceWellKnownPath,
		candidateSourceAppBundle,
		candidateSourceIndirectLauncher,
	}
	allActionKinds = []actionKind{
		actionInstall,
		actionRepair,
		actionSelect,
		actionRemove,
		actionOpenDocs,
	}
	allGateIDs = []string{
		gateG01,
		gateG02,
		gateG03,
		gateG04,
		gateG05,
		gateG06,
		gateG07,
		gateG08,
		gateG09,
	}
	allHostGOOS = []string{
		osLinux,
		osDarwin,
		osWindows,
	}
	allHostGOARCH = []string{
		archAMD64,
		archARM64,
	}
	allVirtualizationCapabilities = []string{
		capKVMAvailable,
		capKVMUnavailable,
		capHypervisorFramework,
		capNotRequired,
		capUnsupported,
	}
	allQualificationStates = []string{
		qualNotRun,
		qualBlocked,
		qualQualified,
	}
	builtinInternalProviderTypes = []string{
		providerDocker,
		providerPodman,
		providerApple,
		providerKubernetes,
		providerSSH,
	}
	canonicalExternalAdapters = map[string]canonicalAdapterBaseline{
		canonicalMicroSandboxRepo: {
			maxPublishedVersion:          semver.MustParse("0.1.11"),
			supportsPathPairRuntimeCheck: false,
		},
		canonicalLimaRepo: {
			maxPublishedVersion:          semver.MustParse("1.1.0"),
			supportsPathPairRuntimeCheck: false,
		},
		canonicalOrbStackRepo: {
			maxPublishedVersion:          semver.MustParse("1.1.4"),
			supportsPathPairRuntimeCheck: false,
		},
	}
)

func TestContractsSuite(t *testing.T) {
	suite.Run(t, new(contractsSuite))
}

func (s *contractsSuite) SetupSuite() {
	s.contractsDir = filepath.Join("testdata", "contracts")
	schema, err := loadSchemaDoc(filepath.Join(s.contractsDir, "schema.json"))
	s.Require().NoError(err)
	s.schema = schema
}

func (s *contractsSuite) TestSchemaAndGoContractCoherence() {
	s.Equal(toStrings(allSurveyModes), s.schema.Properties["surveyMode"].Enum)
	s.Equal(toStrings(allAvailabilityStates), s.schema.Defs["availabilityState"].Enum)
	s.Equal(toStrings(allCompatibilityStates), s.schema.Defs["compatibilityState"].Enum)
	s.Equal(toStrings(allReachabilityStates), s.schema.Defs["reachabilityState"].Enum)
	s.Equal(toStrings(allPolicyStates), s.schema.Defs["policyState"].Enum)
	s.Equal(toStrings(allReasonCodes), s.schema.Defs["reasonCode"].Enum)
	s.Equal(toStrings(allProviderSourceKinds), s.schema.Defs["providerSourceKind"].Enum)
	s.Equal(toStrings(allCandidateSourceKinds), s.schema.Defs["candidateSourceKind"].Enum)
	s.Equal(toStrings(allActionKinds), s.schema.Defs["actionKind"].Enum)
	s.Equal(allGateIDs, s.schema.Defs["gateId"].Enum)
	s.Equal(allHostGOOS, s.schema.Defs["hostIdentity"].Properties["goos"].Enum)
	s.Equal(allHostGOARCH, s.schema.Defs["hostIdentity"].Properties["goarch"].Enum)
	s.Equal(
		allVirtualizationCapabilities,
		s.schema.Defs["hostIdentity"].Properties["virtualizationCapability"].Enum,
	)
	s.Equal(
		allQualificationStates,
		s.schema.Defs["managedRecipeStatus"].Properties["qualificationState"].Enum,
	)
	s.assertAllStructsMatchSchema()
}

func (s *contractsSuite) TestCanonicalReasonCodeVocabularyAndAliasRejection() {
	s.verifyIndependentCanonicalReasonVocabulary()
	s.verifyRemovedReasonAliasesRejected()
	s.verifyCanonicalReasonDimensionSemantics()
}

func (s *contractsSuite) TestCandidateFileObservationIdentityAndSymlinkRecheck() {
	s.verifyAvailableAndMissingCandidateObservations()
	s.verifyCandidateObservationMutationsRejected()
	s.verifySymlinkTargetUpdatePreservesLexicalPathWithDistinctIdentity()
}

func (s *contractsSuite) TestSchemaLoaderRejectsUnsupportedValidationKeywords() {
	s.verifyUnsupportedSchemaKeywordsRejected()
}

func (s *contractsSuite) TestSchemaExactIntegerAndUnicodeLengthConstraints() {
	for _, tc := range buildInexactIntegerSchemaLiteralCases() {
		s.Run(tc.name, func() {
			_, err := parseSchemaBytes([]byte(tc.schema))
			s.Require().ErrorContains(err, tc.errSub)
		})
	}
	cases := slices.Concat(
		buildExactIntegerValidationLiteralCases(),
		buildUnicodeMinLengthLiteralCases(),
	)
	for _, tc := range cases {
		s.Run(tc.name, func() {
			parsed, err := parseSchemaBytes([]byte(tc.schema))
			s.Require().NoError(err)
			err = validateRawJSONAgainstSchema([]byte(tc.input), parsed)
			if tc.errSub == "" {
				s.Require().NoError(err)
				return
			}
			s.Require().ErrorContains(err, tc.errSub)
		})
	}
}

func (s *contractsSuite) TestValidFixturesAndScenarioInvariants() {
	s.verifyCoreFixtures()
	s.verifyLifecycleAndConflictFixtures()
}

func (s *contractsSuite) TestRejectsRawJSONShapeViolations() {
	managedRaw := s.readRawFixture(fixtureManagedDisabled)
	staleRaw := s.readRawFixture(fixtureStaleTarget)

	for _, tc := range buildRootAndRecipeDeletionCases(managedRaw) {
		s.runRawShapeCase(tc)
	}
	for _, tc := range buildItemAndActionDeletionCases(managedRaw, staleRaw) {
		s.runRawShapeCase(tc)
	}
	for _, tc := range buildTypeAndUnknownFieldCases(managedRaw) {
		s.runRawShapeCase(tc)
	}
	s.verifyTrailingJSONRejected(managedRaw)
}

func (s *contractsSuite) TestRejectsMalformedOrMissingIdentity() {
	base := s.loadFixture(fixtureMissing)
	cases := []struct {
		name   string
		mutate func(r *reportContract)
		errSub string
	}{
		{
			name:   "missing contextName",
			mutate: func(r *reportContract) { r.Target.ContextName = "" },
			errSub: "target.contextName is required",
		},
		{
			name:   "missing providerName",
			mutate: func(r *reportContract) { r.Target.ProviderName = "   " },
			errSub: "target.providerName is required",
		},
		{
			name:   "missing providerSourceId",
			mutate: func(r *reportContract) { r.Target.ProviderSourceID = "" },
			errSub: "target.providerSourceId is required",
		},
		{
			name:   "missing providerConfigRevision",
			mutate: func(r *reportContract) { r.Target.ProviderConfigRevision = "" },
			errSub: "target.providerConfigRevision is required",
		},
		{
			name:   "invalid host goos",
			mutate: func(r *reportContract) { r.Host.GOOS = "freebsd" },
			errSub: "invalid host.goos",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			clone := cloneReport(s.T(), base)
			tc.mutate(clone)
			s.Require().ErrorContains(validateReportContract(clone), tc.errSub)
		})
	}
}

func (s *contractsSuite) TestRejectsMalformedNestedEnumsAndMissingCandidateIdentity() {
	stale := s.loadFixture(fixtureStaleTarget)
	cases := slices.Concat(
		buildNestedEnumMutationCases(),
		buildMissingCandidateIdentityCases(),
	)
	for _, tc := range cases {
		s.Run(tc.name, func() {
			clone := cloneReport(s.T(), stale)
			tc.mutate(clone)
			s.Require().ErrorContains(validateReportContract(clone), tc.errSub)

			raw, err := json.Marshal(clone)
			s.Require().NoError(err)
			if tc.expectSchemaErr {
				s.Require().Error(validateRawJSONAgainstSchema(raw, s.schema))
			}
		})
	}
}

func (s *contractsSuite) TestRejectsInconsistentStoppedVsMissing() {
	stopped := s.loadFixture(fixtureStoppedRuntime)

	relabeledMissing := cloneReport(s.T(), stopped)
	relabeledMissing.Availability = availabilityMissing
	s.Require().ErrorContains(
		validateReportContract(relabeledMissing),
		"stopped/unreachable runtime must report availability=\"available\"",
	)

	passiveUnreachable := cloneReport(s.T(), stopped)
	passiveUnreachable.SurveyMode = surveyModePassive
	passiveUnreachable.Executed = false
	s.Require().ErrorContains(
		validateReportContract(passiveUnreachable),
		"passive survey must not report reachability=\"unreachable\"",
	)

	missingWithUnreachableReason := s.loadFixture(fixtureMissing)
	missingWithUnreachableReason.ReasonCode = reasonRuntimeUnreachable
	s.Require().ErrorContains(
		validateReportContract(missingWithUnreachableReason),
		"stopped/unreachable runtime must report availability=\"available\"",
	)
}

func (s *contractsSuite) TestCanonicalSourceSemverCrossAdapterSpoofingAndAliases() {
	s.Run("custom source spoofing built-in name rejected", s.verifyCustomSpoofingRejected)
	s.Run("canonical source with bad semver tag rejected", s.verifyBadCanonicalSemverRejected)
	s.Run("synthetic future adapter release tag rejected", s.verifySyntheticFutureTagRejected)
	s.Run("incoherent adapterVersion or providerType rejected", s.verifyIncoherentTargetRejected)
	s.Run("cross-adapter spoofing with Lima or OrbStack rejected", s.verifyCrossAdapterSpoofing)
	s.Run("legitimate renamed MicroSandbox alias accepted", func() {
		renamed := s.loadFixture(fixtureManagedDisabled)
		renamed.Target.ProviderName = "msb-work-alias"
		s.Require().NoError(validateReportContract(renamed))
	})
}

func (s *contractsSuite) TestRejectsV0111EligibilityAndUnqualifiedManagedActivation() {
	managed := s.loadFixture(fixtureManagedDisabled)

	s.Run("managed_selected policy on v0.1.11 rejected", func() {
		selectedPolicy := cloneReport(s.T(), managed)
		selectedPolicy.Policy = policyManagedSelected
		s.Require().ErrorContains(
			validateReportContract(selectedPolicy),
			"not eligible for policy=\"managed_selected\"",
		)
	})
	s.Run("compatible claim on v0.1.11 in active_probe rejected", func() {
		s.verifyActiveProbeCompatibleOnV0111Rejected(managed)
	})
	s.Run("enabled or qualified recipe rejected", func() {
		s.verifyEnabledOrQualifiedRecipeRejected(managed)
	})
	s.Run("enabled install or select action on v0.1.11 rejected", func() {
		s.verifyEnabledActionsOnV0111Rejected(managed)
	})
	s.Run("mutating action without effect authorization rejected", func() {
		unauthorized := cloneReport(s.T(), managed)
		unauthorized.Actions[0].RequiresEffectAuthorization = false
		s.Require().ErrorContains(
			validateReportContract(unauthorized),
			"must set requiresEffectAuthorization=true",
		)
	})
}

func (s *contractsSuite) TestBundleRelativeFlatLayoutAndLinkInvariants() {
	managed := s.loadFixture(fixtureManagedDisabled)

	s.Run("legitimate DEVSY_HOME ancestors named bin and lib are accepted", func() {
		s.verifyAncestorBinLibAccepted(managed)
	})
	s.Run("nested bundle subdirectories under illustrativeBundleRoot rejected", func() {
		s.verifyNestedBundleSubdirsRejected(managed)
	})
	s.Run("non-flat expectedFiles entries rejected", func() {
		for _, badExpected := range []string{"bin/msb", "lib/libkrunfw.so.5.6.1", ".", "..", "../msb"} {
			bad := cloneReport(s.T(), managed)
			bad.ManagedRecipe.ExpectedFiles = []string{badExpected}
			s.Require().Error(validateReportContract(bad))
		}
	})
	s.Run("dot and parent traversal segments rejected", func() {
		s.verifyTraversalSegmentsRejected(managed)
	})
	s.Run("symlink flag or indirect launcher selection rejected", func() {
		s.verifySymlinkAndShimRejected(managed)
	})
}

func (s *contractsSuite) TestOfflineAssetMissingAndConfigurationConflictSemantics() {
	conflict := s.loadFixture(fixtureConfigConflict)
	offlineMissing := s.loadFixture(fixtureOfflineMissing)

	s.Run("offline_asset_missing rejected on available or external Docker candidate", func() {
		falseOfflineOnDocker := cloneReport(s.T(), conflict)
		falseOfflineOnDocker.Items[0].Candidates[1].ReasonCode = reasonOfflineAssetMissing
		s.Require().ErrorContains(
			validateReportContract(falseOfflineOnDocker),
			"valid only for missing managed_store candidates on canonical MicroSandbox targets",
		)
	})
	s.Run("offline_asset_missing rejected on available managed_store candidate", func() {
		availableOffline := cloneReport(s.T(), offlineMissing)
		availableOffline.Items[0].Candidates[0].Availability = availabilityAvailable
		availableOffline.Items[0].Candidates[0].ResolvedPath = availableOffline.Items[0].Candidates[0].LexicalPath
		availableOffline.Items[0].Candidates[0].Observation = &candidateFileObservation{
			FileIdentity: "illustrative-fid:linux-amd64:msb-store-01",
			Platform:     "linux/amd64",
		}
		s.Require().ErrorContains(
			validateReportContract(availableOffline),
			"valid only for missing managed_store candidates",
		)
	})
	s.Run("offline_asset_missing rejected when install action is enabled", func() {
		enabledOfflineInstall := cloneReport(s.T(), offlineMissing)
		enabledOfflineInstall.Actions[0].Enabled = true
		s.Require().ErrorContains(
			validateReportContract(enabledOfflineInstall),
			"cannot be enabled while all recipes remain unqualified",
		)
	})
	s.Run("configuration_conflict rejected if a conflicting candidate is marked selected", func() {
		selectedConflict := cloneReport(s.T(), conflict)
		selectedConflict.Items[0].Candidates[1].Selected = true
		s.Require().ErrorContains(
			validateReportContract(selectedConflict),
			"configuration conflict item must not select candidate",
		)
	})
	s.Run("unexecuted candidate cannot claim compatible in passive or active reports", func() {
		s.verifyUnexecutedCandidateCompatibleRejected(conflict)
	})
}

func (s *contractsSuite) TestGateIdentitiesAndTupleSeparation() {
	managed := s.loadFixture(fixtureManagedDisabled)

	s.Run("linux tuple rejects macOS G03, update G05, and Desktop skew G08 gates", func() {
		for _, wrongGate := range []string{gateG03, gateG05, gateG08} {
			bad := cloneReport(s.T(), managed)
			bad.ManagedRecipe.BlockingGates = append(
				slices.Clone(bad.ManagedRecipe.BlockingGates),
				wrongGate,
			)
			s.Require().Error(validateReportContract(bad))
		}
	})
	s.Run("fabricated blocker ID in blockingGates rejected by both schema and validator", func() {
		s.verifyFabricatedBlockerIDRejected(managed)
	})
	s.Run("linux/arm64 blocked tuple uses G01/G02/G04/G06/G07/G09 and kvm_unavailable", func() {
		s.verifyLinuxARM64BlockedTuple(managed)
	})
	s.Run(
		"darwin/arm64 candidate tuple uses G01/G02/G03/G06/G07/G09 and rejects Linux G04",
		func() {
			s.verifyDarwinARM64CandidateTuple(managed)
		},
	)
}

func (s *contractsSuite) TestRejectsStaleTargetWithEnabledActionAndPassiveExecution() {
	stale := s.loadFixture(fixtureStaleTarget)
	stale.Actions[0].Enabled = true
	s.Require().ErrorContains(
		validateReportContract(stale),
		"stale target report must not enable mutating action",
	)

	passiveExecuted := s.loadFixture(fixtureMissing)
	passiveExecuted.Executed = true
	s.Require().ErrorContains(
		validateReportContract(passiveExecuted),
		"passive survey must have executed=false",
	)
}

func (s *contractsSuite) TestAllChangedFilesAreASCIIOnly() {
	entries, err := os.ReadDir(s.contractsDir)
	s.Require().NoError(err)

	s.assertFileIsASCIIOnly("contracts_test.go")
	for _, entry := range entries {
		if !entry.IsDir() {
			s.assertFileIsASCIIOnly(filepath.Join(s.contractsDir, entry.Name()))
		}
	}
}

func (s *contractsSuite) TestConfigSchemaVersionRemainsOne() {
	s.Equal(1, config.CurrentSchemaVersion)
}

func (s *contractsSuite) TestBaselineStatusAndErrorEnvelopes() {
	s.T().Setenv("DEVSY_TEST_SECRET_TOKEN", "super-secret-token")
	s.verifyBaselineStatusEnvelope()
	s.verifyBaselineCLIErrorEnvelope()
}

func (s *contractsSuite) decodeAndValidateRaw(raw []byte) (*reportContract, error) {
	if err := validateRawJSONAgainstSchema(raw, s.schema); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var report reportContract
	if err := dec.Decode(&report); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON content")
	}
	if err := validateReportContract(&report); err != nil {
		return nil, err
	}
	return &report, nil
}

func (s *contractsSuite) readRawFixture(filename string) []byte {
	s.T().Helper()
	raw, err := readTestFile(filepath.Join(s.contractsDir, filename))
	s.Require().NoError(err)
	return raw
}

func (s *contractsSuite) loadFixture(filename string) *reportContract {
	s.T().Helper()
	raw := s.readRawFixture(filename)
	report, err := s.decodeAndValidateRaw(raw)
	s.Require().NoError(err, "fixture %s failed schema or contract validation", filename)
	return report
}

func (s *contractsSuite) assertAllStructsMatchSchema() {
	s.assertStructMatchesSchemaObject(reflect.TypeFor[reportContract](), *s.schema)
	s.assertStructMatchesSchemaObject(
		reflect.TypeFor[targetIdentity](),
		s.schema.Defs["targetIdentity"],
	)
	s.assertStructMatchesSchemaObject(
		reflect.TypeFor[hostIdentity](),
		s.schema.Defs["hostIdentity"],
	)
	s.assertStructMatchesSchemaObject(
		reflect.TypeFor[managedRecipeStatus](),
		s.schema.Defs["managedRecipeStatus"],
	)
	s.assertStructMatchesSchemaObject(
		reflect.TypeFor[selectedMicroSandboxPair](),
		s.schema.Defs["selectedMicroSandboxPair"],
	)
	s.assertStructMatchesSchemaObject(
		reflect.TypeFor[candidateFileObservation](),
		s.schema.Defs["candidateFileObservation"],
	)
	s.assertStructMatchesSchemaObject(
		reflect.TypeFor[candidateObservation](),
		s.schema.Defs["candidateObservation"],
	)
	s.assertStructMatchesSchemaObject(
		reflect.TypeFor[dependencyItemReport](),
		s.schema.Defs["dependencyItemReport"],
	)
	s.assertStructMatchesSchemaObject(
		reflect.TypeFor[remediationAction](),
		s.schema.Defs["remediationAction"],
	)
}

func (s *contractsSuite) assertStructMatchesSchemaObject(t reflect.Type, node jsonSchemaNode) {
	s.T().Helper()
	structFields := make([]string, 0, t.NumField())
	requiredByStruct := make([]string, 0, t.NumField())
	for field := range t.Fields() {
		tag := field.Tag.Get("json")
		parts := strings.Split(tag, ",")
		name := parts[0]
		s.Require().NotEmpty(name)
		structFields = append(structFields, name)
		if !slices.Contains(parts[1:], "omitempty") {
			requiredByStruct = append(requiredByStruct, name)
		}
	}
	schemaProps := make([]string, 0, len(node.Properties))
	for k := range node.Properties {
		schemaProps = append(schemaProps, k)
	}
	slices.Sort(structFields)
	slices.Sort(schemaProps)
	s.Equal(schemaProps, structFields, "struct %s fields drifted from schema.json", t.Name())

	expectedReq := slices.Clone(node.Required)
	slices.Sort(expectedReq)
	slices.Sort(requiredByStruct)
	s.Equal(expectedReq, requiredByStruct, "struct %s required fields drifted", t.Name())
}

func (s *contractsSuite) verifyCoreFixtures() {
	missing := s.loadFixture(fixtureMissing)
	s.Equal(surveyModePassive, missing.SurveyMode)
	s.False(missing.Executed)
	s.Equal(availabilityMissing, missing.Availability)
	s.Equal(reachabilityNotChecked, missing.Reachability)
	s.Equal(reasonMissing, missing.ReasonCode)
	s.Nil(missing.Items[0].Candidates[0].Observation)

	external := s.loadFixture(fixtureExternal)
	s.Equal(surveyModeActiveProbe, external.SurveyMode)
	s.True(external.Executed)
	s.Equal(availabilityAvailable, external.Availability)
	s.Equal(compatibilityCompatible, external.Compatibility)
	s.Equal(reachabilityReachable, external.Reachability)
	s.Equal(reasonReady, external.ReasonCode)
	s.Require().Len(external.Items[0].Candidates, 1)
	s.True(external.Items[0].Candidates[0].IsSymlink)
	s.Require().NotNil(external.Items[0].Candidates[0].Observation)
	s.Equal(
		"illustrative-fid:darwin-arm64:docker-28.0.1-cellar-9f2a",
		external.Items[0].Candidates[0].Observation.FileIdentity,
	)

	incomplete := s.loadFixture(fixtureIncompletePair)
	s.Equal(availabilityIncomplete, incomplete.Availability)
	s.Equal(reasonIncomplete, incomplete.ReasonCode)
	s.Nil(incomplete.Items[0].SelectedPair)
	s.Equal(reasonIndirectLauncher, incomplete.Items[0].Candidates[0].ReasonCode)
	s.False(incomplete.Items[0].Candidates[0].Selected)
	s.False(incomplete.Actions[0].Enabled)
	s.Equal(reasonIncomplete, incomplete.Actions[0].DisabledReasonCode)

	managed := s.loadFixture(fixtureManagedDisabled)
	s.Equal(policyManagedAllowed, managed.Policy)
	s.Equal(reasonRecipeUnqualified, managed.ReasonCode)
	s.Require().NotNil(managed.ManagedRecipe)
	s.False(managed.ManagedRecipe.Enabled)
	s.Equal(qualNotRun, managed.ManagedRecipe.QualificationState)
	s.Equal(
		[]string{gateG01, gateG02, gateG04, gateG06, gateG07, gateG09},
		managed.ManagedRecipe.BlockingGates,
	)
	s.Equal(flatArchiveLayoutPolicy, managed.ManagedRecipe.ArchiveLayout)
	s.False(managed.Actions[0].Enabled)
}

func (s *contractsSuite) verifyLifecycleAndConflictFixtures() {
	unsupported := s.loadFixture(fixtureUnsupportedHost)
	s.Equal(osWindows, unsupported.Host.GOOS)
	s.Equal(archAMD64, unsupported.Host.GOARCH)
	s.Equal(capUnsupported, unsupported.Host.VirtualizationCapability)
	s.Equal(policyUnsupported, unsupported.Policy)
	s.Equal(reasonPlatformUnsupported, unsupported.ReasonCode)
	s.Nil(unsupported.ManagedRecipe)

	stopped := s.loadFixture(fixtureStoppedRuntime)
	s.Equal(surveyModeActiveProbe, stopped.SurveyMode)
	s.True(stopped.Executed)
	s.Equal(availabilityAvailable, stopped.Availability)
	s.Equal(reachabilityUnreachable, stopped.Reachability)
	s.Equal(reasonRuntimeUnreachable, stopped.ReasonCode)

	stale := s.loadFixture(fixtureStaleTarget)
	s.Equal("prov-rev-msb-07", stale.Target.ProviderConfigRevision)
	s.Equal("prov-rev-msb-08", stale.Target.StaleAgainstRevision)
	s.Equal(compatibilityUnknown, stale.Compatibility)
	s.Equal(reasonSelectionStale, stale.ReasonCode)
	s.False(stale.Actions[0].Enabled)

	custom := s.loadFixture(fixtureUnknownCustom)
	s.Equal(providerDocker, custom.Target.ProviderName)
	s.False(custom.Target.TrustedBuiltinIdentity)
	s.Equal(policyUnmanagedCustomSource, custom.Policy)
	s.Equal(reasonPolicyBlocked, custom.ReasonCode)

	conflict := s.loadFixture(fixtureConfigConflict)
	s.Equal(reasonConfigurationConflict, conflict.ReasonCode)
	s.Equal(reasonConfigurationConflict, conflict.Items[0].Candidates[0].ReasonCode)
	s.Equal(reasonConfigurationConflict, conflict.Items[0].Candidates[1].ReasonCode)

	offlineMissing := s.loadFixture(fixtureOfflineMissing)
	s.Equal("msb-linux", offlineMissing.Target.ProviderName)
	s.Equal(reasonOfflineAssetMissing, offlineMissing.ReasonCode)
	s.Equal(reasonOfflineAssetMissing, offlineMissing.Items[0].Candidates[0].ReasonCode)
	s.False(offlineMissing.Actions[0].Enabled)
}

func (s *contractsSuite) runRawShapeCase(tc rawShapeCase) {
	s.Run(tc.name, func() {
		var doc map[string]any
		s.Require().NoError(json.Unmarshal(tc.base, &doc))
		tc.mutate(doc)
		mutatedRaw, err := json.Marshal(doc)
		s.Require().NoError(err)

		_, err = s.decodeAndValidateRaw(mutatedRaw)
		s.Require().ErrorContains(err, tc.errSub)
	})
}

func (s *contractsSuite) verifyTrailingJSONRejected(managedRaw []byte) {
	s.Run("trailing JSON document rejected", func() {
		trailingDoc := append(slices.Clone(managedRaw), []byte("\n{}")...)
		_, err := s.decodeAndValidateRaw(trailingDoc)
		s.Require().ErrorContains(err, "trailing JSON content")
	})

	s.Run("trailing scalar token rejected", func() {
		trailingScalar := append(slices.Clone(managedRaw), []byte(" true")...)
		_, err := s.decodeAndValidateRaw(trailingScalar)
		s.Require().ErrorContains(err, "trailing JSON content")
	})
}

func (s *contractsSuite) verifyCustomSpoofingRejected() {
	custom := s.loadFixture(fixtureUnknownCustom)

	spoofTrustedFlag := cloneReport(s.T(), custom)
	spoofTrustedFlag.Target.ProviderName = providerMicroSandbox
	spoofTrustedFlag.Target.ProviderSourceID = "github.com/attacker/devsy-provider-microsandbox@v0.1.11"
	spoofTrustedFlag.Target.TrustedBuiltinIdentity = true
	s.Require().ErrorContains(
		validateReportContract(spoofTrustedFlag),
		"must not set trustedBuiltinIdentity=true",
	)

	spoofPolicy := cloneReport(s.T(), custom)
	spoofPolicy.Policy = policyManagedAllowed
	s.Require().ErrorContains(
		validateReportContract(spoofPolicy),
		"custom provider source must use policy=\"unmanaged_custom_source\"",
	)

	spoofCanonicalKind := cloneReport(s.T(), custom)
	spoofCanonicalKind.Target.ProviderSourceKind = providerSourceCanonicalExternalRelease
	spoofCanonicalKind.Target.ProviderSourceID = "github.com/attacker/devsy-provider-microsandbox@v0.1.11"
	spoofCanonicalKind.Target.TrustedBuiltinIdentity = true
	s.Require().ErrorContains(
		validateReportContract(spoofCanonicalKind),
		"not an approved canonical devsy-org adapter",
	)
}

func (s *contractsSuite) verifyBadCanonicalSemverRejected() {
	managed := s.loadFixture(fixtureManagedDisabled)
	for _, badSource := range []string{
		"github.com/devsy-org/devsy-provider-microsandbox@garbage",
		"github.com/devsy-org/devsy-provider-microsandbox@latest",
		"github.com/devsy-org/devsy-provider-microsandbox@0.1.11",
		"github.com/devsy-org/devsy-provider-microsandbox@v0.1.11-rc.1",
		"github.com/devsy-org/devsy-provider-microsandbox/sub@v0.1.11",
		"github.com/devsy-org/devsy-provider-microsandbox@v0.1.11@extra",
	} {
		bad := cloneReport(s.T(), managed)
		bad.Target.ProviderSourceID = badSource
		s.Require().Error(validateReportContract(bad), "expected error for %s", badSource)
	}
}

func (s *contractsSuite) verifySyntheticFutureTagRejected() {
	managed := s.loadFixture(fixtureManagedDisabled)
	synthetic := cloneReport(s.T(), managed)
	synthetic.Target.ProviderSourceID = "github.com/devsy-org/devsy-provider-microsandbox@v0.1.12"
	synthetic.Target.AdapterVersion = "v0.1.12"
	s.Require().ErrorContains(validateReportContract(synthetic), "unreleased synthetic version")
}

func (s *contractsSuite) verifyIncoherentTargetRejected() {
	managed := s.loadFixture(fixtureManagedDisabled)

	mismatchVer := cloneReport(s.T(), managed)
	mismatchVer.Target.AdapterVersion = "v0.1.10"
	s.Require().ErrorContains(
		validateReportContract(mismatchVer),
		"adapterVersion \"v0.1.10\" must match source tag \"v0.1.11\"",
	)

	wrongProviderType := cloneReport(s.T(), managed)
	wrongProviderType.Target.ProviderType = providerMicroSandbox
	s.Require().ErrorContains(
		validateReportContract(wrongProviderType),
		"must have providerType=\"external\"",
	)

	builtinMismatch := s.loadFixture(fixtureMissing)
	builtinMismatch.Target.ProviderType = providerPodman
	s.Require().ErrorContains(
		validateReportContract(builtinMismatch),
		"builtin_internal source must equal \"internal:podman\"",
	)
}

func (s *contractsSuite) verifyCrossAdapterSpoofing() {
	managed := s.loadFixture(fixtureManagedDisabled)
	stopped := s.loadFixture(fixtureStoppedRuntime)

	limaSpoof := cloneReport(s.T(), managed)
	limaSpoof.Target.ProviderName = providerMicroSandbox
	limaSpoof.Target.ProviderSourceID = "github.com/devsy-org/devsy-provider-lima@v1.1.0"
	limaSpoof.Target.AdapterVersion = "v1.1.0"
	s.Require().ErrorContains(
		validateReportContract(limaSpoof),
		"restricted to canonical external microsandbox source",
	)

	orbSpoofPair := cloneReport(s.T(), stopped)
	orbSpoofPair.Target.ProviderName = providerMicroSandbox
	orbSpoofPair.Items[0].SelectedPair = &selectedMicroSandboxPair{
		MSBLexicalPath:        "/opt/microsandbox/msb",
		MSBResolvedPath:       "/opt/microsandbox/msb",
		LibkrunfwLexicalPath:  "/opt/microsandbox/libkrunfw.so.5.6.1",
		LibkrunfwResolvedPath: "/opt/microsandbox/libkrunfw.so.5.6.1",
		SourceKind:            candidateSourceExplicitOption,
	}
	s.Require().ErrorContains(
		validateReportContract(orbSpoofPair),
		"selectedPair is only valid for canonical MicroSandbox targets",
	)
}

func (s *contractsSuite) verifyActiveProbeCompatibleOnV0111Rejected(managed *reportContract) {
	activeCompat := cloneReport(s.T(), managed)
	activeCompat.SurveyMode = surveyModeActiveProbe
	activeCompat.Executed = true
	activeCompat.Availability = availabilityAvailable
	activeCompat.Compatibility = compatibilityCompatible
	activeCompat.Reachability = reachabilityReachable
	activeCompat.Policy = policyExternalOnly
	activeCompat.ManagedRecipe = nil
	activeCompat.ReasonCode = reasonReady
	s.Require().ErrorContains(
		validateReportContract(activeCompat),
		"unqualified microsandbox adapter lacking T08 runtime-check support",
	)
}

func (s *contractsSuite) verifyEnabledOrQualifiedRecipeRejected(managed *reportContract) {
	enabledRecipe := cloneReport(s.T(), managed)
	enabledRecipe.ManagedRecipe.Enabled = true
	s.Require().ErrorContains(
		validateReportContract(enabledRecipe),
		"cannot be enabled before qualification gates pass",
	)

	qualifiedRecipe := cloneReport(s.T(), managed)
	qualifiedRecipe.ManagedRecipe.QualificationState = qualQualified
	s.Require().ErrorContains(
		validateReportContract(qualifiedRecipe),
		"cannot claim qualificationState=qualified in T00a baseline",
	)
}

func (s *contractsSuite) verifyEnabledActionsOnV0111Rejected(managed *reportContract) {
	enabledInstall := cloneReport(s.T(), managed)
	enabledInstall.Actions[0].Enabled = true
	s.Require().ErrorContains(
		validateReportContract(enabledInstall),
		"cannot be enabled while all recipes remain unqualified",
	)

	incomplete := s.loadFixture(fixtureIncompletePair)
	incomplete.Actions[0].Enabled = true
	s.Require().ErrorContains(validateReportContract(incomplete), "lacking T08 support (G01)")
}

func (s *contractsSuite) verifyAncestorBinLibAccepted(managed *reportContract) {
	bundleRoot := "/home/dev/bin/lib/devsy-home/dependencies/store/microsandbox/v0.7.7-linux-amd64"
	ancestorBinLib := cloneReport(s.T(), managed)
	ancestorBinLib.ManagedRecipe.IllustrativeBundleRoot = bundleRoot
	ancestorBinLib.Items[0].Candidates[0].LexicalPath = bundleRoot + "/msb"
	ancestorBinLib.Items[0].Candidates[1].LexicalPath = bundleRoot + "/libkrunfw.so.5.6.1"
	s.Require().NoError(validateReportContract(ancestorBinLib))
}

func (s *contractsSuite) verifyNestedBundleSubdirsRejected(managed *reportContract) {
	for _, nestedRel := range []string{"bin/msb", "lib/libkrunfw.so.5.6.1", "nested/msb"} {
		nested := cloneReport(s.T(), managed)
		nested.Items[0].Candidates[0].LexicalPath =
			nested.ManagedRecipe.IllustrativeBundleRoot + "/" + nestedRel
		s.Require().ErrorContains(
			validateReportContract(nested),
			"must be flat without subdirectory separators",
		)
	}
}

func (s *contractsSuite) verifyTraversalSegmentsRejected(managed *reportContract) {
	for _, badPath := range []string{
		"/home/dev/.devsy/dependencies/store/microsandbox/v0.7.7-linux-amd64/./msb",
		"/home/dev/.devsy/dependencies/store/microsandbox/v0.7.7-linux-amd64/../msb",
		"/home/dev/.devsy/dependencies/store/../store/microsandbox/v0.7.7-linux-amd64/msb",
	} {
		bad := cloneReport(s.T(), managed)
		bad.Items[0].Candidates[0].LexicalPath = badPath
		s.Require().ErrorContains(
			validateReportContract(bad),
			"dot, or parent path segments",
		)
	}
}

func (s *contractsSuite) verifySymlinkAndShimRejected(managed *reportContract) {
	symlinkCandidate := cloneReport(s.T(), managed)
	symlinkCandidate.Items[0].Candidates[1].IsSymlink = true
	s.Require().ErrorContains(validateReportContract(symlinkCandidate), "must not be a symlink")

	incomplete := s.loadFixture(fixtureIncompletePair)
	selectedShim := cloneReport(s.T(), incomplete)
	selectedShim.Items[0].Candidates[0].Selected = true
	s.Require().ErrorContains(validateReportContract(selectedShim), "indirect launcher candidate")
}

func (s *contractsSuite) verifyFabricatedBlockerIDRejected(managed *reportContract) {
	bad := cloneReport(s.T(), managed)
	bad.ManagedRecipe.BlockingGates = []string{gateG01, gateG04, "linux-arm64-kvm-001"}
	s.Require().ErrorContains(
		validateReportContract(bad),
		"unknown gate \"linux-arm64-kvm-001\"",
	)

	raw, err := json.Marshal(bad)
	s.Require().NoError(err)
	s.Require().ErrorContains(
		validateRawJSONAgainstSchema(raw, s.schema),
		"linux-arm64-kvm-001",
	)
}

func (s *contractsSuite) verifyLinuxARM64BlockedTuple(managed *reportContract) {
	arm64Blocked := cloneReport(s.T(), managed)
	arm64Blocked.Host.GOOS = osLinux
	arm64Blocked.Host.GOARCH = archARM64
	arm64Blocked.Host.VirtualizationCapability = capKVMUnavailable
	arm64Blocked.ManagedRecipe.RecipeID = "microsandbox-runtime-linux-arm64"
	arm64Blocked.ManagedRecipe.Tuple = "linux/arm64"
	arm64Blocked.ManagedRecipe.QualificationState = qualBlocked
	arm64Blocked.ManagedRecipe.BlockingGates = []string{
		gateG01,
		gateG02,
		gateG04,
		gateG06,
		gateG07,
		gateG09,
	}
	s.Require().NoError(validateReportContract(arm64Blocked))
}

func (s *contractsSuite) verifyDarwinARM64CandidateTuple(managed *reportContract) {
	darwinCandidate := cloneReport(s.T(), managed)
	darwinCandidate.Host.GOOS = osDarwin
	darwinCandidate.Host.GOARCH = archARM64
	darwinCandidate.Host.VirtualizationCapability = capHypervisorFramework
	darwinCandidate.ManagedRecipe.RecipeID = "microsandbox-runtime-darwin-arm64"
	darwinCandidate.ManagedRecipe.Tuple = "darwin/arm64"
	darwinCandidate.ManagedRecipe.BlockingGates = []string{
		gateG01,
		gateG02,
		gateG03,
		gateG06,
		gateG07,
		gateG09,
	}
	s.Require().NoError(validateReportContract(darwinCandidate))

	darwinWithLinuxGate := cloneReport(s.T(), darwinCandidate)
	darwinWithLinuxGate.ManagedRecipe.BlockingGates = append(
		slices.Clone(darwinWithLinuxGate.ManagedRecipe.BlockingGates),
		gateG04,
	)
	s.Require().ErrorContains(
		validateReportContract(darwinWithLinuxGate),
		"must not list Linux gate G04",
	)
}

func (s *contractsSuite) assertFileIsASCIIOnly(path string) {
	s.T().Helper()
	raw, err := readTestFile(path)
	s.Require().NoError(err)
	for idx, b := range raw {
		s.Require().Truef(
			isAllowedASCIIByte(b),
			"non-ASCII byte 0x%02x in %s at offset %d",
			b,
			path,
			idx,
		)
	}
}

func (s *contractsSuite) verifyBaselineStatusEnvelope() {
	var statusBuf bytes.Buffer
	failedEvent := status.Event{
		Pipeline:          status.PipelineProvider,
		OperationID:       "op-1",
		ParentOperationID: "op-0",
		Phase:             status.PhaseRunningInit,
		Step:              "Check runtime",
		State:             status.StateFailed,
		Duration:          42 * time.Millisecond,
		Error: &status.ErrorInfo{
			Code:    "provider_init_failed",
			Message: "failed with super-secret-token",
			Hint:    "Verify runtime status",
			Context: map[string]string{contextKeyProvider: providerMicroSandbox},
		},
	}
	s.Require().NoError(devcconfig.WriteStatusJSON(&statusBuf, failedEvent))

	line := strings.TrimSpace(statusBuf.String())
	parsed, ok := devcconfig.ParseStatusLine(line)
	s.Require().True(ok)
	s.Equal(status.PipelineProvider, parsed.Pipeline)
	s.Equal("op-1", parsed.OperationID)
	s.Equal("op-0", parsed.ParentOperationID)
	s.Require().NotNil(parsed.Error)
	s.Equal("failed with ***", parsed.Error.Message)

	var rawMap map[string]any
	s.Require().NoError(json.Unmarshal([]byte(line), &rawMap))
	s.Equal("status", rawMap["kind"])
	s.Equal(float64(1), rawMap["schemaVersion"])
	s.NotContains(rawMap, "sequence")
	s.NotContains(rawMap, "payload")

	_, ok = devcconfig.ParseStatusLine(strings.TrimSuffix(line, "}") + `,"sequence":1}`)
	s.False(ok)
	_, ok = devcconfig.ParseStatusLine(strings.TrimSuffix(line, "}") + `,"payload":{}}`)
	s.False(ok)
}

func (s *contractsSuite) verifyBaselineCLIErrorEnvelope() {
	var errBuf bytes.Buffer
	typedErr := &clierr.CLIError{
		Code:    clierr.Code("provider_init_in_progress"),
		Message: "provider init locked by super-secret-token",
		Hint:    "Wait for the active initialization to finish.",
		Context: map[string]string{contextKeyProvider: providerMicroSandbox},
	}
	s.Equal(75, exitcode.Retryable)
	s.Require().NoError(devcconfig.WriteCLIErrorJSON(&errBuf, typedErr))

	var errEnv devcconfig.ErrorEnvelope
	s.Require().NoError(json.Unmarshal(errBuf.Bytes(), &errEnv))
	s.Equal("error", errEnv.Kind)
	s.Equal("error", errEnv.Outcome)
	s.Equal("provider_init_in_progress", errEnv.Code)
	s.Equal("provider init locked by ***", errEnv.Message)
	s.Equal("Wait for the active initialization to finish.", errEnv.Hint)
	s.Equal(map[string]string{contextKeyProvider: providerMicroSandbox}, errEnv.Context)
}

func (s *contractsSuite) verifyIndependentCanonicalReasonVocabulary() {
	specSuccessReasons := []string{"dependency_ready"}
	specSection63ErrorReasons := []string{
		"dependency_missing",
		"dependency_incomplete",
		"dependency_path_invalid",
		"dependency_version_incompatible",
		"dependency_version_unknown",
		"dependency_platform_unsupported",
		"dependency_policy_blocked",
		"dependency_runtime_unreachable",
		"dependency_auth_required",
		"dependency_probe_timeout",
		"dependency_integrity_failed",
		"dependency_store_unwritable",
		"dependency_recipe_unqualified",
		"dependency_selection_stale",
		"dependency_in_use",
		"dependency_operation_interrupted",
	}
	addendumSection6ErrorReasons := []string{
		"dependency_configuration_conflict",
		"dependency_offline_asset_missing",
		"dependency_indirect_launcher",
	}
	s.Len(specSuccessReasons, 1)
	s.Len(specSection63ErrorReasons, 16)
	s.Len(addendumSection6ErrorReasons, 3)

	expected := slices.Concat(
		specSuccessReasons,
		specSection63ErrorReasons,
		addendumSection6ErrorReasons,
	)
	s.Len(expected, 20)
	s.Equal(expected, s.schema.Defs["reasonCode"].Enum)
	s.Equal(expected, toStrings(allReasonCodes))

	guardMapRaw := string(s.readRawFixture("BASELINE_AND_GUARD_MAP.md"))
	for _, code := range expected {
		s.Contains(guardMapRaw, "`"+code+"`")
	}
}

func (s *contractsSuite) verifyRemovedReasonAliasesRejected() {
	removedAliases := []string{
		"dependency_pair_incomplete",
		"dependency_unsupported_platform",
		"dependency_checksum_mismatch",
		"dependency_archive_rejected",
		"dependency_install_locked",
		"dependency_install_cancelled",
		"dependency_remove_refused_active",
		"dependency_remove_refused_unowned",
		"dependency_unknown_custom_source",
		"dependency_virtualization_unavailable",
	}
	schemaEnum := s.schema.Defs["reasonCode"].Enum
	goEnum := toStrings(allReasonCodes)
	base := s.loadFixture(fixtureMissing)

	for _, alias := range removedAliases {
		s.NotContains(schemaEnum, alias)
		s.NotContains(goEnum, alias)
		s.assertRemovedAliasRejectedAcrossPositions(base, reasonCode(alias))
	}
}

func (s *contractsSuite) assertRemovedAliasRejectedAcrossPositions(
	base *reportContract,
	alias reasonCode,
) {
	s.T().Helper()
	mutators := []func(*reportContract){
		func(r *reportContract) { r.ReasonCode = alias },
		func(r *reportContract) { r.Items[0].ReasonCode = alias },
		func(r *reportContract) { r.Items[0].Candidates[0].ReasonCode = alias },
		func(r *reportContract) {
			r.Actions[0].Enabled = false
			r.Actions[0].DisabledReasonCode = alias
		},
	}
	for _, mutate := range mutators {
		m := cloneReport(s.T(), base)
		mutate(m)
		s.Require().Error(validateReportContract(m))
		raw, err := json.Marshal(m)
		s.Require().NoError(err)
		s.Require().ErrorContains(validateRawJSONAgainstSchema(raw, s.schema), string(alias))
	}
}

func (s *contractsSuite) verifyCanonicalReasonDimensionSemantics() {
	s.verifyValidCanonicalReasonScenarios()
	s.verifyInvalidCanonicalReasonDimensionCollapse()
}

func (s *contractsSuite) verifyValidCanonicalReasonScenarios() {
	missing := s.loadFixture(fixtureMissing)
	pathInvalid := cloneReport(s.T(), missing)
	pathInvalid.ReasonCode = reasonPathInvalid
	pathInvalid.Items[0].ReasonCode = reasonPathInvalid
	pathInvalid.Items[0].Candidates[0].ReasonCode = reasonPathInvalid
	s.assertValidMutatedReport(pathInvalid)

	external := s.loadFixture(fixtureExternal)
	verIncompat := cloneReport(s.T(), external)
	verIncompat.Compatibility = compatibilityIncompatible
	verIncompat.Reachability = reachabilityNotChecked
	verIncompat.ReasonCode = reasonVersionIncompatible
	verIncompat.Items[0].Compatibility = compatibilityIncompatible
	verIncompat.Items[0].Reachability = reachabilityNotChecked
	verIncompat.Items[0].ReasonCode = reasonVersionIncompatible
	verIncompat.Items[0].Candidates[0].Compatibility = compatibilityIncompatible
	verIncompat.Items[0].Candidates[0].ReasonCode = reasonVersionIncompatible
	s.assertValidMutatedReport(verIncompat)

	s.verifyValidAuthRequiredScenarios(external)
	s.verifyValidActiveAndActionReasonScenarios()
}

func (s *contractsSuite) verifyValidAuthRequiredScenarios(external *reportContract) {
	passiveAuth := buildPassiveAuthRequiredReport(s.T(), external)
	s.assertValidMutatedReport(passiveAuth)

	preExecBlocked := cloneReport(s.T(), passiveAuth)
	preExecBlocked.SurveyMode = surveyModeActiveProbe
	s.assertValidMutatedReport(preExecBlocked)

	activeNotChecked := cloneReport(s.T(), external)
	activeNotChecked.Reachability = reachabilityNotChecked
	activeNotChecked.ReasonCode = reasonAuthRequired
	activeNotChecked.Items[0].Reachability = reachabilityNotChecked
	activeNotChecked.Items[0].ReasonCode = reasonAuthRequired
	s.assertValidMutatedReport(activeNotChecked)
}

func (s *contractsSuite) verifyValidActiveAndActionReasonScenarios() {
	stopped := s.loadFixture(fixtureStoppedRuntime)
	authReq := cloneReport(s.T(), stopped)
	authReq.ReasonCode = reasonAuthRequired
	authReq.Items[0].ReasonCode = reasonAuthRequired
	s.assertValidMutatedReport(authReq)

	probeTimeout := cloneReport(s.T(), stopped)
	probeTimeout.Compatibility = compatibilityUnknown
	probeTimeout.Reachability = reachabilityUnreachable
	probeTimeout.ReasonCode = reasonProbeTimeout
	probeTimeout.Items[0].Compatibility = compatibilityUnknown
	probeTimeout.Items[0].Reachability = reachabilityUnreachable
	probeTimeout.Items[0].ReasonCode = reasonProbeTimeout
	s.assertValidMutatedReport(probeTimeout)

	incomplete := s.loadFixture(fixtureIncompletePair)
	for _, actionReason := range []reasonCode{
		reasonIntegrityFailed,
		reasonStoreUnwritable,
		reasonInUse,
		reasonOperationInterrupted,
		reasonPolicyBlocked,
	} {
		m := cloneReport(s.T(), incomplete)
		m.Actions[0].Enabled = false
		m.Actions[0].DisabledReasonCode = actionReason
		s.assertValidMutatedReport(m)
	}
}

func (s *contractsSuite) verifyInvalidCanonicalReasonDimensionCollapse() {
	external := s.loadFixture(fixtureExternal)

	badVerIncompat := cloneReport(s.T(), external)
	badVerIncompat.ReasonCode = reasonVersionIncompatible
	s.Require().ErrorContains(
		validateReportContract(badVerIncompat),
		`requires availability="available" and compatibility="incompatible"`,
	)

	badVerUnknown := cloneReport(s.T(), external)
	badVerUnknown.ReasonCode = reasonVersionUnknown
	s.Require().ErrorContains(
		validateReportContract(badVerUnknown),
		`requires availability="available" and compatibility="unknown"`,
	)

	badIncomplete := cloneReport(s.T(), external)
	badIncomplete.ReasonCode = reasonIncomplete
	s.Require().ErrorContains(
		validateReportContract(badIncomplete),
		`requires availability="incomplete"`,
	)

	badPlatform := cloneReport(s.T(), external)
	badPlatform.ReasonCode = reasonPlatformUnsupported
	s.Require().ErrorContains(
		validateReportContract(badPlatform),
		`requires policy="unsupported"`,
	)

	badPathInvalid := cloneReport(s.T(), external)
	badPathInvalid.ReasonCode = reasonPathInvalid
	s.Require().ErrorContains(
		validateReportContract(badPathInvalid),
		`cannot report availability="available"`,
	)

	missing := s.loadFixture(fixtureMissing)
	s.verifyInvalidAuthRequiredScenarios(external, missing)

	badTimeout := cloneReport(s.T(), missing)
	badTimeout.ReasonCode = reasonProbeTimeout
	s.Require().ErrorContains(validateReportContract(badTimeout), "requires active_probe")
}

func (s *contractsSuite) verifyInvalidAuthRequiredScenarios(
	external, missing *reportContract,
) {
	badMissingAuth := cloneReport(s.T(), missing)
	badMissingAuth.ReasonCode = reasonAuthRequired
	s.Require().ErrorContains(
		validateReportContract(badMissingAuth),
		`requires availability="available"`,
	)

	passiveAuth := buildPassiveAuthRequiredReport(s.T(), external)

	passiveExecuted := cloneReport(s.T(), passiveAuth)
	passiveExecuted.Executed = true
	s.Require().ErrorContains(
		validateReportContract(passiveExecuted),
		"passive survey must have executed=false",
	)

	passiveUnreachable := cloneReport(s.T(), passiveAuth)
	passiveUnreachable.Reachability = reachabilityUnreachable
	s.Require().ErrorContains(
		validateReportContract(passiveUnreachable),
		`passive survey must not report reachability="unreachable"`,
	)

	unexecCompat := cloneReport(s.T(), passiveAuth)
	unexecCompat.SurveyMode = surveyModeActiveProbe
	unexecCompat.Compatibility = compatibilityCompatible
	s.Require().ErrorContains(
		validateReportContract(unexecCompat),
		`unexecuted reasonCode="dependency_auth_required" requires compatibility="unknown" and reachability="not_checked"`,
	)

	reachableAuth := cloneReport(s.T(), external)
	reachableAuth.ReasonCode = reasonAuthRequired
	s.Require().ErrorContains(
		validateReportContract(reachableAuth),
		`cannot report reachability="reachable"`,
	)
}

func (s *contractsSuite) assertValidMutatedReport(r *reportContract) {
	s.T().Helper()
	raw, err := json.Marshal(r)
	s.Require().NoError(err)
	_, err = s.decodeAndValidateRaw(raw)
	s.Require().NoError(err)
}

func (s *contractsSuite) verifyAvailableAndMissingCandidateObservations() {
	fixtures := []string{
		fixtureMissing,
		fixtureExternal,
		fixtureIncompletePair,
		fixtureManagedDisabled,
		fixtureUnsupportedHost,
		fixtureStoppedRuntime,
		fixtureStaleTarget,
		fixtureUnknownCustom,
		fixtureConfigConflict,
		fixtureOfflineMissing,
	}
	availableCount := 0
	missingCount := 0
	for _, filename := range fixtures {
		avail, miss := s.verifyFixtureCandidateObservations(filename)
		availableCount += avail
		missingCount += miss
	}
	s.Positive(availableCount)
	s.Positive(missingCount)
}

func (s *contractsSuite) verifyFixtureCandidateObservations(filename string) (int, int) {
	s.T().Helper()
	report := s.loadFixture(filename)
	avail := 0
	miss := 0
	for _, item := range report.Items {
		for _, cand := range item.Candidates {
			a, m := s.verifySingleCandidateObservation(filename, &report.Target, &cand)
			avail += a
			miss += m
		}
	}
	return avail, miss
}

func (s *contractsSuite) verifySingleCandidateObservation(
	filename string,
	target *targetIdentity,
	cand *candidateObservation,
) (int, int) {
	s.T().Helper()
	if cand.Availability == availabilityAvailable {
		s.Require().NotNil(cand.Observation, "fixture %s candidate %s", filename, cand.LexicalPath)
		s.NotEmpty(cand.Observation.FileIdentity)
		s.NotEqual(cand.LexicalPath, cand.Observation.FileIdentity)
		s.NotEqual(cand.ResolvedPath, cand.Observation.FileIdentity)
		s.NotEqual(target.ConfigRevision, cand.Observation.FileIdentity)
		s.NotEqual(target.ProviderConfigRevision, cand.Observation.FileIdentity)
		return 1, 0
	}
	if cand.Availability == availabilityMissing {
		s.Nil(cand.Observation, "fixture %s missing candidate %s", filename, cand.LexicalPath)
		return 0, 1
	}
	return 0, 0
}

func (s *contractsSuite) verifyCandidateObservationMutationsRejected() {
	external := s.loadFixture(fixtureExternal)

	missingObs := cloneReport(s.T(), external)
	missingObs.Items[0].Candidates[0].Observation = nil
	s.Require().ErrorContains(
		validateReportContract(missingObs),
		"must record non-empty observation.fileIdentity",
	)

	pathIdentity := cloneReport(s.T(), external)
	pathIdentity.Items[0].Candidates[0].Observation.FileIdentity = pathIdentity.Items[0].Candidates[0].ResolvedPath
	s.Require().ErrorContains(
		validateReportContract(pathIdentity),
		"distinct from lexicalPath and resolvedPath",
	)

	revIdentity := cloneReport(s.T(), external)
	revIdentity.Items[0].Candidates[0].Observation.FileIdentity = revIdentity.Target.ProviderConfigRevision
	s.Require().ErrorContains(
		validateReportContract(revIdentity),
		"distinct from target config/provider revisions",
	)

	missing := s.loadFixture(fixtureMissing)
	fabricatedMissing := cloneReport(s.T(), missing)
	fabricatedMissing.Items[0].Candidates[0].Observation = &candidateFileObservation{
		FileIdentity: "obs-file:linux-amd64:fabricated-01",
	}
	s.Require().ErrorContains(
		validateReportContract(fabricatedMissing),
		"must not fabricate an observation record",
	)

	incomplete := s.loadFixture(fixtureIncompletePair)
	passiveVersion := cloneReport(s.T(), incomplete)
	passiveVersion.Items[0].Candidates[1].Observation.ObservedVersion = "0.7.7"
	s.Require().ErrorContains(
		validateReportContract(passiveVersion),
		"must not claim observation.observedVersion",
	)

	externalRaw := s.readRawFixture(fixtureExternal)
	s.runRawShapeCase(rawShapeCase{
		name: "available candidate observation missing fileIdentity rejected by schema",
		base: externalRaw,
		mutate: func(doc map[string]any) {
			obs := firstCandidateMap(doc)["observation"].(map[string]any)
			delete(obs, "fileIdentity")
		},
		errSub: `$.items[0].candidates[0].observation: missing required field "fileIdentity"`,
	})
}

func (s *contractsSuite) verifySymlinkTargetUpdatePreservesLexicalPathWithDistinctIdentity() {
	before := s.loadFixture(fixtureExternal)
	beforeCand := before.Items[0].Candidates[0]

	after := cloneReport(s.T(), before)
	after.SurveyMode = surveyModePassive
	after.Executed = false
	after.Compatibility = compatibilityUnknown
	after.Reachability = reachabilityNotChecked
	after.ReasonCode = reasonVersionUnknown
	after.Items[0].Executed = false
	after.Items[0].Compatibility = compatibilityUnknown
	after.Items[0].Reachability = reachabilityNotChecked
	after.Items[0].ReasonCode = reasonVersionUnknown

	afterCand := &after.Items[0].Candidates[0]
	afterCand.Executed = false
	afterCand.Compatibility = compatibilityUnknown
	afterCand.ReasonCode = reasonVersionUnknown
	afterCand.ResolvedPath = "/opt/homebrew/Cellar/docker/28.0.2/bin/docker"
	afterCand.Observation = &candidateFileObservation{
		FileIdentity: "illustrative-fid:darwin-arm64:docker-28.0.2-cellar-a41c",
		Platform:     "darwin/arm64",
	}

	s.Equal(beforeCand.LexicalPath, afterCand.LexicalPath)
	s.True(afterCand.IsSymlink)
	s.NotEqual(beforeCand.Observation.FileIdentity, afterCand.Observation.FileIdentity)
	s.assertValidMutatedReport(after)

	prematureCompat := cloneReport(s.T(), after)
	prematureCompat.Compatibility = compatibilityCompatible
	s.Require().ErrorContains(
		validateReportContract(prematureCompat),
		"must not claim verified compatibility",
	)
}

func (s *contractsSuite) verifyUnexecutedCandidateCompatibleRejected(conflict *reportContract) {
	passiveCandidateCompat := cloneReport(s.T(), conflict)
	passiveCandidateCompat.Items[0].Candidates[1].Compatibility = compatibilityCompatible
	s.Require().ErrorContains(
		validateReportContract(passiveCandidateCompat),
		"unexecuted candidate \"/usr/bin/docker\" (executed=false) must not claim verified compatibility=\"compatible\"",
	)
	rawPassive, err := json.Marshal(passiveCandidateCompat)
	s.Require().NoError(err)
	_, err = s.decodeAndValidateRaw(rawPassive)
	s.Require().ErrorContains(
		err,
		"unexecuted candidate \"/usr/bin/docker\" (executed=false) must not claim verified compatibility=\"compatible\"",
	)

	activeWithUnprobed := s.buildActiveReportWithUnprobedSecondaryCandidate()
	s.assertValidMutatedReport(activeWithUnprobed)

	activeUnprobedCompat := cloneReport(s.T(), activeWithUnprobed)
	activeUnprobedCompat.Items[0].Candidates[1].Compatibility = compatibilityCompatible
	s.Require().ErrorContains(
		validateReportContract(activeUnprobedCompat),
		"unexecuted candidate \"/usr/local/bin/docker\" (executed=false) "+
			"must not claim verified compatibility=\"compatible\"",
	)
}

func (s *contractsSuite) buildActiveReportWithUnprobedSecondaryCandidate() *reportContract {
	active := s.loadFixture(fixtureExternal)
	active.Items[0].Candidates = append(active.Items[0].Candidates, candidateObservation{
		Component:    "docker",
		SourceKind:   candidateSourceWellKnownPath,
		OptionName:   "DOCKER_PATH",
		LexicalPath:  "/usr/local/bin/docker",
		ResolvedPath: "/usr/local/bin/docker",
		Observation: &candidateFileObservation{
			FileIdentity: "illustrative-fid:darwin-arm64:usr-local-docker-77b1",
			Platform:     active.Items[0].Candidates[0].Observation.Platform,
		},
		Executed:      false,
		Selected:      false,
		Availability:  availabilityAvailable,
		Compatibility: compatibilityUnknown,
		ReasonCode:    reasonVersionUnknown,
		Detail:        "Unprobed secondary candidate on well-known path.",
	})
	return active
}

func (s *contractsSuite) verifyUnsupportedSchemaKeywordsRejected() {
	schemaRaw := s.readRawFixture("schema.json")
	_, err := parseSchemaBytes(schemaRaw)
	s.Require().NoError(err)
	s.verifyRefWithMetadataOnlySiblingAccepted(schemaRaw)
	s.verifyZeroLimitsAccepted(schemaRaw)

	cases := slices.Concat(
		buildUnsupportedSchemaKeywordCases(),
		buildIgnoredSchemaKeywordCombinationCases(),
		buildInvalidSchemaConstraintValueCases(),
	)
	for _, tc := range cases {
		s.Run(tc.name, func() {
			var doc map[string]any
			s.Require().NoError(json.Unmarshal(schemaRaw, &doc))
			tc.mutate(doc)
			mutated, err := json.Marshal(doc)
			s.Require().NoError(err)
			_, err = parseSchemaBytes(mutated)
			s.Require().ErrorContains(err, tc.errSub)
		})
	}
}

func (s *contractsSuite) verifyRefWithMetadataOnlySiblingAccepted(schemaRaw []byte) {
	var doc map[string]any
	s.Require().NoError(json.Unmarshal(schemaRaw, &doc))
	avail := schemaRootPropertyMap(doc, "availability")
	avail["title"] = "AvailabilityDim"
	avail["description"] = "Documented $ref without validation siblings."
	mutated, err := json.Marshal(doc)
	s.Require().NoError(err)

	parsed, err := parseSchemaBytes(mutated)
	s.Require().NoError(err)
	s.Require().NoError(validateRawJSONAgainstSchema(s.readRawFixture(fixtureMissing), parsed))
}

func (s *contractsSuite) verifyZeroLimitsAccepted(schemaRaw []byte) {
	var doc map[string]any
	s.Require().NoError(json.Unmarshal(schemaRaw, &doc))
	schemaRootPropertyMap(doc, "message")["minLength"] = 0
	schemaRootPropertyMap(doc, "items")["minItems"] = 0
	relaxedRaw, err := json.Marshal(doc)
	s.Require().NoError(err)
	relaxed, err := parseSchemaBytes(relaxedRaw)
	s.Require().NoError(err)

	var report map[string]any
	s.Require().NoError(json.Unmarshal(s.readRawFixture(fixtureMissing), &report))
	report["message"] = ""
	report["items"] = []any{}
	reportRaw, err := json.Marshal(report)
	s.Require().NoError(err)

	s.Require().NoError(validateRawJSONAgainstSchema(reportRaw, relaxed))
	baseline, err := parseSchemaBytes(schemaRaw)
	s.Require().NoError(err)
	s.Require().Error(validateRawJSONAgainstSchema(reportRaw, baseline))
}

func readTestFile(relPath string) ([]byte, error) {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	return fs.ReadFile(os.DirFS("."), clean)
}

func isAllowedASCIIByte(b byte) bool {
	if b == '\n' || b == '\r' || b == '\t' {
		return true
	}
	return b >= 0x20 && b <= 0x7e
}

func loadSchemaDoc(path string) (*jsonSchemaNode, error) {
	raw, err := readTestFile(path)
	if err != nil {
		return nil, err
	}
	return parseSchemaBytes(raw)
}

func parseSchemaBytes(raw []byte) (*jsonSchemaNode, error) {
	rawDec := json.NewDecoder(bytes.NewReader(raw))
	rawDec.UseNumber()
	var rawDoc any
	if err := rawDec.Decode(&rawDoc); err != nil {
		return nil, err
	}
	if err := validateSchemaNodeKeywords(rawDoc, "$"); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var root jsonSchemaNode
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON content in schema")
	}
	return &root, nil
}

func validateSchemaNodeKeywords(raw any, path string) error {
	nodeMap, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: schema node must be an object, got %T", path, raw)
	}
	keys := slices.Sorted(maps.Keys(nodeMap))
	for _, k := range keys {
		if !slices.Contains(allowedSchemaNodeKeywords, k) {
			return fmt.Errorf("%s: unsupported schema keyword %q", path, k)
		}
	}
	if err := validateSchemaKeywordContext(nodeMap, keys, path); err != nil {
		return err
	}
	for _, k := range keys {
		if err := validateSchemaChildKeyword(k, nodeMap[k], path); err != nil {
			return err
		}
	}
	return nil
}

// validateSchemaKeywordContext rejects keyword/type combinations that
// validateSchemaValue would otherwise skip without evaluating them.
func validateSchemaKeywordContext(nodeMap map[string]any, keys []string, path string) error {
	if _, isRef := nodeMap[keywordRef]; isRef {
		return validateSchemaRefNode(keys, path)
	}
	typeName, _ := nodeMap[keywordType].(string)
	supported, known := schemaKeywordsByType[typeName]
	if !known {
		return fmt.Errorf(
			"%s: schema node requires type object, array, string, integer or boolean, got %v",
			path,
			nodeMap[keywordType],
		)
	}
	if err := validateSchemaTypedKeywords(keys, supported, typeName, path); err != nil {
		return err
	}
	return validateSchemaConstraintValues(nodeMap, typeName, path)
}

func validateSchemaTypedKeywords(keys, supported []string, typeName, path string) error {
	for _, k := range keys {
		if slices.Contains(schemaMetadataKeywords, k) || k == keywordType {
			continue
		}
		if k == keywordDefs && path == "$" {
			continue
		}
		if !slices.Contains(supported, k) {
			return fmt.Errorf(
				"%s: keyword %q is not supported on schema type %q",
				path,
				k,
				typeName,
			)
		}
	}
	return nil
}

func validateSchemaRefNode(keys []string, path string) error {
	for _, k := range keys {
		if k != keywordRef && !slices.Contains(schemaMetadataKeywords, k) {
			return fmt.Errorf("%s: $ref node cannot combine with validation keyword %q", path, k)
		}
	}
	return nil
}

func validateSchemaConstraintValues(nodeMap map[string]any, typeName, path string) error {
	if err := validateSchemaConst(nodeMap, typeName, path); err != nil {
		return err
	}
	if enum, ok := nodeMap[keywordEnum]; ok {
		if err := validateSchemaEnum(enum, path+"."+keywordEnum); err != nil {
			return err
		}
	}
	for _, keyword := range []string{keywordMinLength, keywordMinItems} {
		if limit, ok := nodeMap[keyword]; ok {
			if err := validateSchemaLimit(limit, path+"."+keyword); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSchemaConst(nodeMap map[string]any, typeName, path string) error {
	val, ok := nodeMap[keywordConst]
	if !ok {
		return nil
	}
	switch typeName {
	case schemaTypeString:
		if _, isString := val.(string); !isString {
			return fmt.Errorf("%s.const: expected string const for type %q", path, typeName)
		}
	case schemaTypeInteger:
		if _, isInteger := schemaIntegerToken(val); !isInteger {
			return fmt.Errorf("%s.const: expected integer const for type %q", path, typeName)
		}
	}
	return nil
}

func validateSchemaEnum(val any, path string) error {
	values, ok := val.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("%s: expected non-empty string array", path)
	}
	for _, v := range values {
		if _, isString := v.(string); !isString {
			return fmt.Errorf("%s: expected non-empty string array", path)
		}
	}
	return nil
}

// validateSchemaLimit accepts zero: minLength and minItems of 0 are valid
// no-op limits that the evaluator handles correctly.
func validateSchemaLimit(val any, path string) error {
	limit, isInteger := schemaIntegerToken(val)
	if !isInteger || limit < 0 {
		return fmt.Errorf("%s: expected non-negative integer", path)
	}
	return nil
}

// schemaIntegerToken accepts only plain JSON integer tokens within int64.
// Fraction or exponent forms are rejected rather than rounded so a schema
// constraint can never silently differ from its written value.
func schemaIntegerToken(val any) (int64, bool) {
	num, ok := val.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := num.Int64()
	return parsed, err == nil
}

func validateSchemaChildKeyword(key string, val any, path string) error {
	switch key {
	case keywordDefs, keywordProperties:
		subMap, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.%s: expected object map, got %T", path, key, val)
		}
		for subKey, subVal := range subMap {
			if err := validateSchemaNodeKeywords(subVal, path+"."+key+"."+subKey); err != nil {
				return err
			}
		}
	case keywordItems:
		return validateSchemaNodeKeywords(val, path+"."+keywordItems)
	}
	return nil
}

func validateRawJSONAgainstSchema(raw []byte, rootSchema *jsonSchemaNode) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON content after root document")
	}

	return validateSchemaValue(rootSchema, rootSchema, "$", doc)
}

func validateSchemaValue(root, node *jsonSchemaNode, path string, val any) error {
	if node.Ref != "" {
		return validateSchemaRef(root, node.Ref, path, val)
	}
	switch node.Type {
	case schemaTypeObject:
		return validateSchemaObject(root, node, path, val)
	case schemaTypeArray:
		return validateSchemaArray(root, node, path, val)
	case schemaTypeString:
		return validateSchemaString(node, path, val)
	case schemaTypeInteger:
		return validateSchemaInteger(node, path, val)
	case schemaTypeBoolean:
		return validateSchemaBoolean(path, val)
	default:
		return fmt.Errorf("%s: unsupported schema type %q", path, node.Type)
	}
}

func validateSchemaRef(root *jsonSchemaNode, ref, path string, val any) error {
	const defPrefix = "#/$defs/"
	if !strings.HasPrefix(ref, defPrefix) {
		return fmt.Errorf("%s: unsupported schema $ref %q", path, ref)
	}
	defName := strings.TrimPrefix(ref, defPrefix)
	target, ok := root.Defs[defName]
	if !ok {
		return fmt.Errorf("%s: unknown schema $ref %q", path, ref)
	}
	return validateSchemaValue(root, &target, path, val)
}

func validateSchemaObject(root, node *jsonSchemaNode, path string, val any) error {
	obj, ok := val.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: expected object, got %T", path, val)
	}
	if err := checkRequiredAndUnknownFields(node, path, obj); err != nil {
		return err
	}
	for k, v := range obj {
		propSchema, declared := node.Properties[k]
		if !declared {
			continue
		}
		if err := validateSchemaValue(root, &propSchema, path+"."+k, v); err != nil {
			return err
		}
	}
	return nil
}

func checkRequiredAndUnknownFields(
	node *jsonSchemaNode,
	path string,
	obj map[string]any,
) error {
	for _, req := range node.Required {
		if _, exists := obj[req]; !exists {
			return fmt.Errorf("%s: missing required field %q", path, req)
		}
	}
	if node.AdditionalProperties == nil || *node.AdditionalProperties {
		return nil
	}
	for k := range obj {
		if _, declared := node.Properties[k]; !declared {
			return fmt.Errorf("%s: unknown field %q", path, k)
		}
	}
	return nil
}

func validateSchemaArray(root, node *jsonSchemaNode, path string, val any) error {
	arr, ok := val.([]any)
	if !ok {
		return fmt.Errorf("%s: expected array, got %T", path, val)
	}
	if len(arr) < node.MinItems {
		return fmt.Errorf("%s: array length %d is below minItems %d", path, len(arr), node.MinItems)
	}
	if node.Items == nil {
		return nil
	}
	for i, elem := range arr {
		elemPath := fmt.Sprintf("%s[%d]", path, i)
		if err := validateSchemaValue(root, node.Items, elemPath, elem); err != nil {
			return err
		}
	}
	return nil
}

func validateSchemaString(node *jsonSchemaNode, path string, val any) error {
	s, ok := val.(string)
	if !ok {
		return fmt.Errorf("%s: expected string, got %T", path, val)
	}
	if length := utf8.RuneCountInString(s); length < node.MinLength {
		return fmt.Errorf(
			"%s: string length %d is below minLength %d",
			path,
			length,
			node.MinLength,
		)
	}
	if constStr, ok := node.Const.(string); ok && s != constStr {
		return fmt.Errorf("%s: expected const %q, got %q", path, constStr, s)
	}
	if len(node.Enum) > 0 && !slices.Contains(node.Enum, s) {
		return fmt.Errorf("%s: value %q is not in enum %v", path, s, node.Enum)
	}
	return nil
}

func validateSchemaInteger(node *jsonSchemaNode, path string, val any) error {
	num, ok := val.(json.Number)
	if !ok || strings.ContainsAny(num.String(), ".eE") {
		return fmt.Errorf("%s: expected integer, got %v (%T)", path, val, val)
	}
	parsedInt, err := num.Int64()
	if err != nil {
		return fmt.Errorf("%s: invalid integer %s: %w", path, num.String(), err)
	}
	if node.Const == nil {
		return nil
	}
	constInt, ok := schemaIntegerToken(node.Const)
	if !ok {
		return fmt.Errorf("%s: unsupported integer const %v", path, node.Const)
	}
	if parsedInt != constInt {
		return fmt.Errorf("%s: expected const %d, got %d", path, constInt, parsedInt)
	}
	return nil
}

func validateSchemaBoolean(path string, val any) error {
	if _, ok := val.(bool); !ok {
		return fmt.Errorf("%s: expected boolean, got %T", path, val)
	}
	return nil
}

func validateReportContract(r *reportContract) error {
	if err := validateReportHeader(r); err != nil {
		return err
	}
	ctx, err := buildTargetContext(r)
	if err != nil {
		return err
	}
	if err := validateHostIdentity(&r.Host); err != nil {
		return err
	}
	if err := validateDimensions(ctx, reportDimensions(r)); err != nil {
		return err
	}
	if err := validateManagedRecipe(ctx); err != nil {
		return err
	}
	return validateReportItemsAndActions(ctx)
}

func validateReportHeader(r *reportContract) error {
	if r == nil {
		return fmt.Errorf("report is nil")
	}
	if r.Schema != reportSchemaID {
		return fmt.Errorf("unexpected schema %q", r.Schema)
	}
	if r.SchemaVersion != reportSchemaVersion {
		return fmt.Errorf("unexpected schemaVersion %d", r.SchemaVersion)
	}
	if !slices.Contains(allSurveyModes, r.SurveyMode) {
		return fmt.Errorf("invalid surveyMode %q", r.SurveyMode)
	}
	if strings.TrimSpace(r.Message) == "" {
		return fmt.Errorf("message is required")
	}
	return nil
}

func reportDimensions(r *reportContract) dimensionSet {
	return dimensionSet{
		mode:     r.SurveyMode,
		executed: r.Executed,
		avail:    r.Availability,
		compat:   r.Compatibility,
		reach:    r.Reachability,
		policy:   r.Policy,
		reason:   r.ReasonCode,
	}
}

func itemDimensions(r *reportContract, item *dependencyItemReport) dimensionSet {
	return dimensionSet{
		mode:     r.SurveyMode,
		executed: item.Executed,
		avail:    item.Availability,
		compat:   item.Compatibility,
		reach:    item.Reachability,
		policy:   item.Policy,
		reason:   item.ReasonCode,
	}
}

func validateReportItemsAndActions(ctx targetContext) error {
	r := ctx.report
	if len(r.Items) == 0 {
		return fmt.Errorf("items must not be empty")
	}
	for i := range r.Items {
		if err := validateItem(ctx, &r.Items[i]); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
	}
	for i := range r.Actions {
		if err := validateAction(ctx, &r.Actions[i]); err != nil {
			return fmt.Errorf("actions[%d]: %w", i, err)
		}
	}
	return nil
}

func buildTargetContext(r *reportContract) (targetContext, error) {
	if err := validateRequiredTargetFields(&r.Target); err != nil {
		return targetContext{}, err
	}
	ctx, err := classifyTargetSource(r)
	if err != nil {
		return targetContext{}, err
	}
	if err := validateTargetStaleness(r); err != nil {
		return targetContext{}, err
	}
	return ctx, nil
}

func validateRequiredTargetFields(t *targetIdentity) error {
	required := []struct {
		field string
		val   string
	}{
		{"target.contextName", t.ContextName},
		{"target.providerName", t.ProviderName},
		{"target.providerType", t.ProviderType},
		{"target.providerSourceId", t.ProviderSourceID},
		{"target.configRevision", t.ConfigRevision},
		{"target.providerConfigRevision", t.ProviderConfigRevision},
		{"target.evaluatedAt", t.EvaluatedAt},
	}
	for _, item := range required {
		if strings.TrimSpace(item.val) == "" {
			return fmt.Errorf("%s is required", item.field)
		}
	}
	if !slices.Contains(allProviderSourceKinds, t.ProviderSourceKind) {
		return fmt.Errorf("invalid target.providerSourceKind %q", t.ProviderSourceKind)
	}
	return nil
}

func classifyTargetSource(r *reportContract) (targetContext, error) {
	switch r.Target.ProviderSourceKind {
	case providerSourceBuiltinInternal:
		return validateBuiltinInternalTarget(r)
	case providerSourceCanonicalExternalRelease:
		return validateCanonicalExternalTarget(r)
	case providerSourceCustomGitHub, providerSourceCustomLocal, providerSourceCustomURL:
		return validateCustomTarget(r)
	default:
		return targetContext{}, fmt.Errorf(
			"unsupported source kind %q",
			r.Target.ProviderSourceKind,
		)
	}
}

func validateBuiltinInternalTarget(r *reportContract) (targetContext, error) {
	t := &r.Target
	if !slices.Contains(builtinInternalProviderTypes, t.ProviderType) {
		return targetContext{}, fmt.Errorf(
			"builtin_internal target.providerType %q must be one of %v",
			t.ProviderType,
			builtinInternalProviderTypes,
		)
	}
	expectedSourceID := "internal:" + t.ProviderType
	if t.ProviderSourceID != expectedSourceID {
		return targetContext{}, fmt.Errorf(
			"builtin_internal source must equal %q for providerType %q, got %q",
			expectedSourceID,
			t.ProviderType,
			t.ProviderSourceID,
		)
	}
	if t.AdapterVersion != "" {
		return targetContext{}, fmt.Errorf(
			"builtin_internal source must not set adapterVersion, got %q",
			t.AdapterVersion,
		)
	}
	if !t.TrustedBuiltinIdentity {
		return targetContext{}, fmt.Errorf(
			"builtin_internal source must have trustedBuiltinIdentity=true",
		)
	}
	return targetContext{report: r}, nil
}

func validateCanonicalExternalTarget(r *reportContract) (targetContext, error) {
	t := &r.Target
	spec, err := parseCanonicalExternalSource(t.ProviderSourceID)
	if err != nil {
		return targetContext{}, err
	}
	baseline := canonicalExternalAdapters[spec.repo]
	if spec.ver.GT(baseline.maxPublishedVersion) {
		return targetContext{}, fmt.Errorf(
			"canonical source %q references unreleased synthetic version %q (latest published is v%s)",
			t.ProviderSourceID,
			spec.tag,
			baseline.maxPublishedVersion.String(),
		)
	}
	if t.ProviderType != providerTypeExternal {
		return targetContext{}, fmt.Errorf(
			"canonical_external_release must have providerType=%q, got %q",
			providerTypeExternal,
			t.ProviderType,
		)
	}
	if t.AdapterVersion != spec.tag {
		return targetContext{}, fmt.Errorf(
			"canonical_external_release adapterVersion %q must match source tag %q",
			t.AdapterVersion,
			spec.tag,
		)
	}
	if !t.TrustedBuiltinIdentity {
		return targetContext{}, fmt.Errorf(
			"canonical_external_release source must have trustedBuiltinIdentity=true",
		)
	}
	return targetContext{
		report:             r,
		isCanonicalMSB:     spec.repo == canonicalMicroSandboxRepo,
		adapterSupportsT08: baseline.supportsPathPairRuntimeCheck,
	}, nil
}

func parseCanonicalExternalSource(sourceID string) (canonicalSourceSpec, error) {
	parts := strings.Split(sourceID, "@")
	if len(parts) != 2 {
		return canonicalSourceSpec{}, fmt.Errorf(
			"canonical_external_release source %q must have exact <repo>@<vMAJOR.MINOR.PATCH> format",
			sourceID,
		)
	}
	repo, tag := parts[0], parts[1]
	if _, ok := canonicalExternalAdapters[repo]; !ok {
		return canonicalSourceSpec{}, fmt.Errorf(
			"canonical_external_release repository %q is not an approved canonical devsy-org adapter",
			repo,
		)
	}
	ver, err := parsePinnedReleaseSemver(tag)
	if err != nil {
		return canonicalSourceSpec{}, err
	}
	return canonicalSourceSpec{repo: repo, tag: tag, ver: ver}, nil
}

func parsePinnedReleaseSemver(tag string) (semver.Version, error) {
	if !strings.HasPrefix(tag, "v") {
		return semver.Version{}, fmt.Errorf(
			"canonical_external_release tag %q must have v-prefixed pinned semver",
			tag,
		)
	}
	ver, err := semver.Parse(strings.TrimPrefix(tag, "v"))
	if err != nil || !isCleanReleaseVersion(ver, tag) {
		return semver.Version{}, fmt.Errorf(
			"canonical_external_release tag %q is not a valid pinned release semver",
			tag,
		)
	}
	return ver, nil
}

func isCleanReleaseVersion(ver semver.Version, tag string) bool {
	if len(ver.Pre) > 0 || len(ver.Build) > 0 {
		return false
	}
	return "v"+ver.String() == tag
}

func validateCustomTarget(r *reportContract) (targetContext, error) {
	if r.Target.TrustedBuiltinIdentity {
		return targetContext{}, fmt.Errorf(
			"custom provider source %q must not set trustedBuiltinIdentity=true",
			r.Target.ProviderSourceID,
		)
	}
	if r.Policy != policyUnmanagedCustomSource {
		return targetContext{}, fmt.Errorf(
			"custom provider source must use policy=%q, got %q",
			policyUnmanagedCustomSource,
			r.Policy,
		)
	}
	if r.ReasonCode != reasonPolicyBlocked {
		return targetContext{}, fmt.Errorf(
			"custom provider source must use reasonCode=%q, got %q",
			reasonPolicyBlocked,
			r.ReasonCode,
		)
	}
	if r.ManagedRecipe != nil {
		return targetContext{}, fmt.Errorf("custom provider source must not attach a managedRecipe")
	}
	return targetContext{report: r}, nil
}

func validateTargetStaleness(r *reportContract) error {
	t := &r.Target
	if t.StaleAgainstRevision == "" || t.StaleAgainstRevision == t.ProviderConfigRevision {
		return nil
	}
	if r.ReasonCode != reasonSelectionStale {
		return fmt.Errorf(
			"stale target revision requires reasonCode=%q, got %q",
			reasonSelectionStale,
			r.ReasonCode,
		)
	}
	return nil
}

func validateHostIdentity(h *hostIdentity) error {
	if !slices.Contains(allHostGOOS, h.GOOS) {
		return fmt.Errorf("invalid host.goos %q", h.GOOS)
	}
	if !slices.Contains(allHostGOARCH, h.GOARCH) {
		return fmt.Errorf("invalid host.goarch %q", h.GOARCH)
	}
	if !slices.Contains(allVirtualizationCapabilities, h.VirtualizationCapability) {
		return fmt.Errorf("invalid host.virtualizationCapability %q", h.VirtualizationCapability)
	}
	return nil
}

func validateDimensions(ctx targetContext, d dimensionSet) error {
	if err := validateDimensionEnums(d); err != nil {
		return err
	}
	if err := validatePassiveSurveyDimensions(d); err != nil {
		return err
	}
	if err := validateUnqualifiedMSBDimensions(ctx, d); err != nil {
		return err
	}
	if err := validateCanonicalReasonDimensions(d); err != nil {
		return err
	}
	return validateReachabilityAndMissingDimensions(d)
}

func validateDimensionEnums(d dimensionSet) error {
	if !slices.Contains(allAvailabilityStates, d.avail) {
		return fmt.Errorf("invalid availability %q", d.avail)
	}
	if !slices.Contains(allCompatibilityStates, d.compat) {
		return fmt.Errorf("invalid compatibility %q", d.compat)
	}
	if !slices.Contains(allReachabilityStates, d.reach) {
		return fmt.Errorf("invalid reachability %q", d.reach)
	}
	if !slices.Contains(allPolicyStates, d.policy) {
		return fmt.Errorf("invalid policy %q", d.policy)
	}
	if !slices.Contains(allReasonCodes, d.reason) {
		return fmt.Errorf("invalid reasonCode %q", d.reason)
	}
	return nil
}

func validatePassiveSurveyDimensions(d dimensionSet) error {
	if d.mode != surveyModePassive {
		return nil
	}
	if d.executed {
		return fmt.Errorf("passive survey must have executed=false")
	}
	if d.reach == reachabilityReachable || d.reach == reachabilityUnreachable {
		return fmt.Errorf("passive survey must not report reachability=%q", d.reach)
	}
	if d.compat == compatibilityCompatible {
		return fmt.Errorf(
			"passive survey must not claim verified compatibility=%q without executing a probe",
			d.compat,
		)
	}
	return nil
}

func validateUnqualifiedMSBDimensions(ctx targetContext, d dimensionSet) error {
	if !ctx.isCanonicalMSB || ctx.adapterSupportsT08 {
		return nil
	}
	if d.compat == compatibilityCompatible {
		return fmt.Errorf(
			"unqualified microsandbox adapter lacking T08 runtime-check support cannot report compatibility=%q",
			d.compat,
		)
	}
	if d.policy == policyManagedSelected {
		return fmt.Errorf(
			"unqualified microsandbox adapter lacking T08 path-pair support is not eligible for policy=%q",
			d.policy,
		)
	}
	return nil
}

func validateCanonicalReasonDimensions(d dimensionSet) error {
	if err := validateCanonicalAvailabilityAndPolicyReason(d); err != nil {
		return err
	}
	if err := validateCanonicalVersionReason(d); err != nil {
		return err
	}
	return validateCanonicalProbeReason(d)
}

func validateCanonicalAvailabilityAndPolicyReason(d dimensionSet) error {
	if d.reason == reasonIncomplete && d.avail != availabilityIncomplete {
		return fmt.Errorf(
			"reasonCode=%q requires availability=%q, got %q",
			reasonIncomplete,
			availabilityIncomplete,
			d.avail,
		)
	}
	if d.reason == reasonPathInvalid && d.avail == availabilityAvailable {
		return fmt.Errorf(
			"reasonCode=%q cannot report availability=%q",
			reasonPathInvalid,
			availabilityAvailable,
		)
	}
	if d.reason == reasonPlatformUnsupported && d.policy != policyUnsupported {
		return fmt.Errorf(
			"reasonCode=%q requires policy=%q, got %q",
			reasonPlatformUnsupported,
			policyUnsupported,
			d.policy,
		)
	}
	return nil
}

func validateCanonicalVersionReason(d dimensionSet) error {
	expectedCompat, ok := requiredCompatibilityForReason(d.reason)
	if !ok {
		return nil
	}
	if d.avail != availabilityAvailable || d.compat != expectedCompat {
		return fmt.Errorf(
			"reasonCode=%q requires availability=%q and compatibility=%q",
			d.reason,
			availabilityAvailable,
			expectedCompat,
		)
	}
	return nil
}

func requiredCompatibilityForReason(r reasonCode) (compatibilityState, bool) {
	switch r {
	case reasonReady:
		return compatibilityCompatible, true
	case reasonVersionIncompatible:
		return compatibilityIncompatible, true
	case reasonVersionUnknown:
		return compatibilityUnknown, true
	default:
		return "", false
	}
}

func validateCanonicalProbeReason(d dimensionSet) error {
	if d.reason == reasonAuthRequired {
		return validateAuthRequiredReason(d)
	}
	if d.reason == reasonProbeTimeout && !isActiveProbeAvailable(d) {
		return fmt.Errorf(
			"reasonCode=%q requires active_probe, executed=true, and availability=%q",
			reasonProbeTimeout,
			availabilityAvailable,
		)
	}
	return nil
}

func validateAuthRequiredReason(d dimensionSet) error {
	if d.avail != availabilityAvailable {
		return fmt.Errorf(
			"reasonCode=%q requires availability=%q, got %q",
			reasonAuthRequired,
			availabilityAvailable,
			d.avail,
		)
	}
	if d.reach == reachabilityReachable {
		return fmt.Errorf(
			"reasonCode=%q cannot report reachability=%q",
			reasonAuthRequired,
			reachabilityReachable,
		)
	}
	if !d.executed && !hasUnexecutedUnknownState(d) {
		return fmt.Errorf(
			"unexecuted reasonCode=%q requires compatibility=%q and reachability=%q",
			reasonAuthRequired,
			compatibilityUnknown,
			reachabilityNotChecked,
		)
	}
	return nil
}

func hasUnexecutedUnknownState(d dimensionSet) bool {
	return d.compat == compatibilityUnknown && d.reach == reachabilityNotChecked
}

func isActiveProbeAvailable(d dimensionSet) bool {
	return isActiveProbeExecution(d) && d.avail == availabilityAvailable
}

func isActiveProbeExecution(d dimensionSet) bool {
	return d.executed && d.mode == surveyModeActiveProbe
}

func validateReachabilityAndMissingDimensions(d dimensionSet) error {
	if err := validateUnreachableDimensions(d); err != nil {
		return err
	}
	return validateMissingDimensions(d)
}

func validateUnreachableDimensions(d dimensionSet) error {
	if !isUnreachableStateOrReason(d) {
		return nil
	}
	if d.avail != availabilityAvailable {
		return fmt.Errorf(
			"stopped/unreachable runtime must report availability=%q, got %q",
			availabilityAvailable,
			d.avail,
		)
	}
	if d.reach != reachabilityUnreachable {
		return fmt.Errorf(
			"reasonCode=%q requires reachability=%q, got %q",
			reasonRuntimeUnreachable,
			reachabilityUnreachable,
			d.reach,
		)
	}
	if !isActiveProbeExecution(d) {
		return fmt.Errorf(
			"stopped/unreachable runtime requires surveyMode=%q and executed=true",
			surveyModeActiveProbe,
		)
	}
	return nil
}

func isUnreachableStateOrReason(d dimensionSet) bool {
	return d.reach == reachabilityUnreachable || d.reason == reasonRuntimeUnreachable
}

func validateMissingDimensions(d dimensionSet) error {
	if d.avail != availabilityMissing {
		return nil
	}
	if d.reach == reachabilityReachable || d.reach == reachabilityUnreachable {
		return fmt.Errorf("missing dependency cannot report reachability=%q", d.reach)
	}
	if d.reason == reasonRuntimeUnreachable || d.reason == reasonReady {
		return fmt.Errorf("missing dependency cannot report reasonCode=%q", d.reason)
	}
	return nil
}

func validateManagedRecipe(ctx targetContext) error {
	if err := validateRecipePolicyEligibility(ctx); err != nil {
		return err
	}
	r := ctx.report
	if r.ManagedRecipe == nil {
		return nil
	}
	if err := validateRecipeFields(r); err != nil {
		return err
	}
	if err := validateRecipeBlockingGates(r.ManagedRecipe, ctx.adapterSupportsT08); err != nil {
		return err
	}
	return validateRecipeBundlePaths(r.ManagedRecipe)
}

func validateRecipePolicyEligibility(ctx targetContext) error {
	r := ctx.report
	if isManagedPolicyOrRecipePresent(r) && !ctx.isCanonicalMSB {
		return fmt.Errorf(
			"managed policy and recipes are restricted to canonical external microsandbox source %q, got %q",
			canonicalMicroSandboxRepo,
			r.Target.ProviderSourceID,
		)
	}
	if err := validateUnsupportedPolicyRecipe(r); err != nil {
		return err
	}
	if err := validateManagedAllowedRecipe(ctx); err != nil {
		return err
	}
	return validateOfflineMissingRecipe(ctx)
}

func isManagedPolicyOrRecipePresent(r *reportContract) bool {
	return r.Policy == policyManagedAllowed || r.Policy == policyManagedSelected ||
		r.ManagedRecipe != nil
}

func validateUnsupportedPolicyRecipe(r *reportContract) error {
	if r.Policy != policyUnsupported {
		return nil
	}
	if r.ManagedRecipe != nil {
		return fmt.Errorf("unsupported host policy must not attach a managedRecipe")
	}
	if r.ReasonCode != reasonPlatformUnsupported {
		return fmt.Errorf(
			"policy=%q requires reasonCode=%q, got %q",
			policyUnsupported,
			reasonPlatformUnsupported,
			r.ReasonCode,
		)
	}
	return nil
}

func validateManagedAllowedRecipe(ctx targetContext) error {
	r := ctx.report
	if r.Policy != policyManagedAllowed {
		return nil
	}
	if r.ManagedRecipe == nil {
		return fmt.Errorf("policy=%q requires a managedRecipe definition", policyManagedAllowed)
	}
	if !ctx.adapterSupportsT08 && !isAllowedUntestedManagedReason(r.ReasonCode) {
		return fmt.Errorf(
			"prospective managed_allowed report on adapter %q without T08 support must report reasonCode=%q or %q",
			r.Target.AdapterVersion,
			reasonRecipeUnqualified,
			reasonOfflineAssetMissing,
		)
	}
	return nil
}

func isAllowedUntestedManagedReason(rc reasonCode) bool {
	return rc == reasonRecipeUnqualified || rc == reasonOfflineAssetMissing
}

func validateOfflineMissingRecipe(ctx targetContext) error {
	if ctx.report.ReasonCode != reasonOfflineAssetMissing {
		return nil
	}
	if !hasDisabledCanonicalMSBRecipe(ctx) {
		return fmt.Errorf(
			"reasonCode=%q requires a canonical MicroSandbox target with a disabled prospective managedRecipe",
			reasonOfflineAssetMissing,
		)
	}
	return nil
}

func hasDisabledCanonicalMSBRecipe(ctx targetContext) bool {
	r := ctx.report
	return ctx.isCanonicalMSB && r.ManagedRecipe != nil && !r.ManagedRecipe.Enabled
}

func validateRecipeFields(r *reportContract) error {
	mr := r.ManagedRecipe
	if strings.TrimSpace(mr.RecipeID) == "" || strings.TrimSpace(mr.Tuple) == "" {
		return fmt.Errorf("managedRecipe.recipeId and tuple are required")
	}
	expectedTuple := r.Host.GOOS + "/" + r.Host.GOARCH
	if mr.Tuple != expectedTuple {
		return fmt.Errorf("managedRecipe.tuple %q must match host %q", mr.Tuple, expectedTuple)
	}
	if mr.ArchiveLayout != flatArchiveLayoutPolicy {
		return fmt.Errorf(
			"managedRecipe.archiveLayout must be %q, got %q",
			flatArchiveLayoutPolicy,
			mr.ArchiveLayout,
		)
	}
	if !slices.Contains(allQualificationStates, mr.QualificationState) {
		return fmt.Errorf("invalid managedRecipe.qualificationState %q", mr.QualificationState)
	}
	if mr.Enabled {
		return fmt.Errorf(
			"managedRecipe %q cannot be enabled before qualification gates pass",
			mr.RecipeID,
		)
	}
	if mr.QualificationState == qualQualified {
		return fmt.Errorf(
			"managedRecipe %q cannot claim qualificationState=qualified in T00a baseline",
			mr.RecipeID,
		)
	}
	return nil
}

func validateRecipeBlockingGates(mr *managedRecipeStatus, adapterSupportsT08 bool) error {
	if err := validateGateListAndG01(mr, adapterSupportsT08); err != nil {
		return err
	}
	if err := validateLinuxAndDarwinTupleGates(mr); err != nil {
		return err
	}
	return validateNonInitialTupleGates(mr)
}

func validateGateListAndG01(mr *managedRecipeStatus, adapterSupportsT08 bool) error {
	if len(mr.BlockingGates) == 0 {
		return fmt.Errorf("disabled managedRecipe %q must list blockingGates", mr.RecipeID)
	}
	for _, g := range mr.BlockingGates {
		if !slices.Contains(allGateIDs, g) {
			return fmt.Errorf("managedRecipe.blockingGates contains unknown gate %q", g)
		}
	}
	if !adapterSupportsT08 && !slices.Contains(mr.BlockingGates, gateG01) {
		return fmt.Errorf(
			"managedRecipe %q must include G01 in blockingGates while adapter lacks T08 support",
			mr.RecipeID,
		)
	}
	return nil
}

func validateLinuxAndDarwinTupleGates(mr *managedRecipeStatus) error {
	if strings.HasPrefix(mr.Tuple, "linux/") {
		if !slices.Contains(mr.BlockingGates, gateG04) {
			return fmt.Errorf(
				"linux managedRecipe %q must include G04 in blockingGates",
				mr.RecipeID,
			)
		}
		if slices.Contains(mr.BlockingGates, gateG03) {
			return fmt.Errorf("linux managedRecipe %q must not list macOS gate G03", mr.RecipeID)
		}
	}
	if strings.HasPrefix(mr.Tuple, "darwin/") {
		if !slices.Contains(mr.BlockingGates, gateG03) {
			return fmt.Errorf(
				"darwin managedRecipe %q must include G03 in blockingGates",
				mr.RecipeID,
			)
		}
		if slices.Contains(mr.BlockingGates, gateG04) {
			return fmt.Errorf("darwin managedRecipe %q must not list Linux gate G04", mr.RecipeID)
		}
	}
	return nil
}

func validateNonInitialTupleGates(mr *managedRecipeStatus) error {
	if slices.Contains(mr.BlockingGates, gateG05) {
		return fmt.Errorf(
			"single-version recipe %q must not list two-version update gate G05",
			mr.RecipeID,
		)
	}
	if slices.Contains(mr.BlockingGates, gateG08) {
		return fmt.Errorf("recipe %q must not list Desktop/CLI version skew gate G08", mr.RecipeID)
	}
	return nil
}

func validateRecipeBundlePaths(mr *managedRecipeStatus) error {
	if mr.IllustrativeBundleRoot != "" {
		if err := validateCleanAbsoluteSlashPath(
			mr.IllustrativeBundleRoot,
			"managedRecipe.illustrativeBundleRoot",
		); err != nil {
			return err
		}
	}
	if len(mr.ExpectedFiles) == 0 {
		return fmt.Errorf("managedRecipe.expectedFiles must not be empty")
	}
	for _, f := range mr.ExpectedFiles {
		if err := validateFlatFilename(f, "managedRecipe.expectedFiles"); err != nil {
			return err
		}
	}
	return nil
}

func validateItem(ctx targetContext, item *dependencyItemReport) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.DisplayName) == "" {
		return fmt.Errorf("item id and displayName are required")
	}
	if err := validateDimensions(ctx, itemDimensions(ctx.report, item)); err != nil {
		return err
	}
	if err := validateSelectedPair(ctx, item); err != nil {
		return err
	}
	for i := range item.Candidates {
		if err := validateCandidate(ctx, item, &item.Candidates[i]); err != nil {
			return fmt.Errorf("candidates[%d]: %w", i, err)
		}
	}
	return nil
}

func validateSelectedPair(ctx targetContext, item *dependencyItemReport) error {
	if item.SelectedPair == nil {
		return nil
	}
	if item.ReasonCode == reasonConfigurationConflict {
		return fmt.Errorf("configuration conflict item must not report a selectedPair")
	}
	if !ctx.isCanonicalMSB {
		return fmt.Errorf("selectedPair is only valid for canonical MicroSandbox targets")
	}
	sp := item.SelectedPair
	if !hasAllSelectedPairPaths(sp) {
		return fmt.Errorf("selectedPair requires non-empty lexical and resolved paths")
	}
	if !slices.Contains(allCandidateSourceKinds, sp.SourceKind) {
		return fmt.Errorf("invalid selectedPair.sourceKind %q", sp.SourceKind)
	}
	if sp.SourceKind == candidateSourceIndirectLauncher {
		return fmt.Errorf("selectedPair must not use an indirect_launcher source")
	}
	return validateSelectedPairManagedStore(ctx, sp)
}

func hasAllSelectedPairPaths(sp *selectedMicroSandboxPair) bool {
	return strings.TrimSpace(sp.MSBLexicalPath) != "" &&
		strings.TrimSpace(sp.MSBResolvedPath) != "" &&
		strings.TrimSpace(sp.LibkrunfwLexicalPath) != "" &&
		strings.TrimSpace(sp.LibkrunfwResolvedPath) != ""
}

func validateSelectedPairManagedStore(ctx targetContext, sp *selectedMicroSandboxPair) error {
	if sp.SourceKind != candidateSourceManagedStore {
		return nil
	}
	if !ctx.adapterSupportsT08 {
		return fmt.Errorf("selectedPair cannot use managed_store before T08/G01 qualification")
	}
	mr := ctx.report.ManagedRecipe
	if err := validateManagedStoreBundleEntry(
		mr,
		sp.MSBLexicalPath,
		sp.MSBResolvedPath,
		false,
	); err != nil {
		return err
	}
	return validateManagedStoreBundleEntry(
		mr,
		sp.LibkrunfwLexicalPath,
		sp.LibkrunfwResolvedPath,
		false,
	)
}

func validateCandidate(
	ctx targetContext,
	item *dependencyItemReport,
	c *candidateObservation,
) error {
	if err := validateCandidateIdentity(c); err != nil {
		return err
	}
	if err := validateCandidateEnumsAndMode(ctx, c); err != nil {
		return err
	}
	if err := validateCandidateOptionAndPath(c); err != nil {
		return err
	}
	if err := validateCandidateObservation(ctx, c); err != nil {
		return err
	}
	if err := validateCandidateSelectionAndCompat(ctx, item, c); err != nil {
		return err
	}
	return validateCandidateSourceRules(ctx, c)
}

func validateCandidateIdentity(c *candidateObservation) error {
	if strings.TrimSpace(c.Component) == "" {
		return fmt.Errorf("candidate.component is required")
	}
	if strings.TrimSpace(c.LexicalPath) == "" {
		return fmt.Errorf("candidate.lexicalPath is required")
	}
	if !slices.Contains(allCandidateSourceKinds, c.SourceKind) {
		return fmt.Errorf("invalid candidate.sourceKind %q", c.SourceKind)
	}
	return nil
}

func validateCandidateEnumsAndMode(ctx targetContext, c *candidateObservation) error {
	if !slices.Contains(allAvailabilityStates, c.Availability) {
		return fmt.Errorf("invalid candidate.availability %q", c.Availability)
	}
	if !slices.Contains(allCompatibilityStates, c.Compatibility) {
		return fmt.Errorf("invalid candidate.compatibility %q", c.Compatibility)
	}
	if !slices.Contains(allReasonCodes, c.ReasonCode) {
		return fmt.Errorf("invalid candidate.reasonCode %q", c.ReasonCode)
	}
	if ctx.report.SurveyMode == surveyModePassive && c.Executed {
		return fmt.Errorf("passive survey candidate %q must have executed=false", c.LexicalPath)
	}
	return nil
}

func validateCandidateOptionAndPath(c *candidateObservation) error {
	if requiresCandidateOptionName(c.SourceKind) && strings.TrimSpace(c.OptionName) == "" {
		return fmt.Errorf("candidate sourceKind=%q requires non-empty optionName", c.SourceKind)
	}
	if c.Availability == availabilityAvailable && strings.TrimSpace(c.ResolvedPath) == "" {
		return fmt.Errorf("available candidate %q must record resolvedPath", c.LexicalPath)
	}
	return nil
}

func validateCandidateObservation(ctx targetContext, c *candidateObservation) error {
	if c.Availability == availabilityMissing {
		return validateMissingCandidateObservation(c)
	}
	if c.Availability != availabilityAvailable {
		return nil
	}
	return validateAvailableCandidateObservation(ctx, c)
}

func validateMissingCandidateObservation(c *candidateObservation) error {
	if c.Observation != nil {
		return fmt.Errorf(
			"missing candidate %q must not fabricate an observation record",
			c.LexicalPath,
		)
	}
	return nil
}

func validateAvailableCandidateObservation(ctx targetContext, c *candidateObservation) error {
	if c.Observation == nil || strings.TrimSpace(c.Observation.FileIdentity) == "" {
		return fmt.Errorf(
			"available candidate %q must record non-empty observation.fileIdentity",
			c.LexicalPath,
		)
	}
	if err := validateOpaqueFileIdentity(&ctx.report.Target, c); err != nil {
		return err
	}
	if !c.Executed && strings.TrimSpace(c.Observation.ObservedVersion) != "" {
		return fmt.Errorf(
			"passive candidate %q (executed=false) must not claim observation.observedVersion %q without an authorized probe",
			c.LexicalPath,
			c.Observation.ObservedVersion,
		)
	}
	return nil
}

func validateOpaqueFileIdentity(t *targetIdentity, c *candidateObservation) error {
	fid := c.Observation.FileIdentity
	if fid == c.LexicalPath || fid == c.ResolvedPath {
		return fmt.Errorf(
			"candidate %q observation.fileIdentity %q must be distinct from lexicalPath and resolvedPath",
			c.LexicalPath,
			fid,
		)
	}
	if isTargetRevisionCollision(t, fid) {
		return fmt.Errorf(
			"candidate %q observation.fileIdentity %q must be distinct from target config/provider revisions",
			c.LexicalPath,
			fid,
		)
	}
	return nil
}

func isTargetRevisionCollision(t *targetIdentity, fid string) bool {
	if fid == t.ConfigRevision || fid == t.ProviderConfigRevision {
		return true
	}
	return t.StaleAgainstRevision != "" && fid == t.StaleAgainstRevision
}

func requiresCandidateOptionName(sk candidateSourceKind) bool {
	return sk == candidateSourceExplicitOption || sk == candidateSourceExplicitEnv
}

func validateCandidateSelectionAndCompat(
	ctx targetContext,
	item *dependencyItemReport,
	c *candidateObservation,
) error {
	if c.Selected && c.Availability != availabilityAvailable {
		return fmt.Errorf("selected candidate %q must have availability=available", c.LexicalPath)
	}
	if item.ReasonCode == reasonConfigurationConflict && c.Selected {
		return fmt.Errorf("configuration conflict item must not select candidate %q", c.LexicalPath)
	}
	if ctx.isCanonicalMSB && !ctx.adapterSupportsT08 && c.Compatibility == compatibilityCompatible {
		return fmt.Errorf(
			"candidate %q on unqualified microsandbox adapter cannot claim compatible",
			c.LexicalPath,
		)
	}
	return validateUnexecutedCandidateCompat(c)
}

// validateUnexecutedCandidateCompat applies per candidate, not per report: an
// active report may still contain candidates that were never probed.
func validateUnexecutedCandidateCompat(c *candidateObservation) error {
	if c.Executed || c.Compatibility != compatibilityCompatible {
		return nil
	}
	return fmt.Errorf(
		"unexecuted candidate %q (executed=false) must not claim verified compatibility=%q",
		c.LexicalPath,
		c.Compatibility,
	)
}

func validateCandidateSourceRules(ctx targetContext, c *candidateObservation) error {
	if err := validateIndirectLauncherCandidate(c); err != nil {
		return err
	}
	if err := validateOfflineMissingCandidate(ctx, c); err != nil {
		return err
	}
	return validateManagedStoreCandidate(ctx, c)
}

func validateIndirectLauncherCandidate(c *candidateObservation) error {
	if c.SourceKind != candidateSourceIndirectLauncher {
		return nil
	}
	if c.Selected {
		return fmt.Errorf("indirect launcher candidate %q must not be selected", c.LexicalPath)
	}
	if c.ReasonCode != reasonIndirectLauncher {
		return fmt.Errorf(
			"indirect launcher candidate %q must use reasonCode=%q, got %q",
			c.LexicalPath,
			reasonIndirectLauncher,
			c.ReasonCode,
		)
	}
	return nil
}

func validateOfflineMissingCandidate(ctx targetContext, c *candidateObservation) error {
	if c.ReasonCode != reasonOfflineAssetMissing {
		return nil
	}
	if !ctx.isCanonicalMSB || c.SourceKind != candidateSourceManagedStore ||
		c.Availability != availabilityMissing {
		return fmt.Errorf(
			"reasonCode=%q is valid only for missing managed_store candidates on canonical MicroSandbox targets",
			reasonOfflineAssetMissing,
		)
	}
	return nil
}

func validateManagedStoreCandidate(ctx targetContext, c *candidateObservation) error {
	if c.SourceKind != candidateSourceManagedStore {
		return nil
	}
	if !ctx.isCanonicalMSB {
		return fmt.Errorf(
			"managed_store candidate is valid only for canonical MicroSandbox targets",
		)
	}
	if !ctx.adapterSupportsT08 && (c.Selected || c.Availability == availabilityAvailable) {
		return fmt.Errorf(
			"managed_store candidate %q cannot be available or selected before T08/G01 qualification",
			c.LexicalPath,
		)
	}
	return validateManagedStoreBundleEntry(
		ctx.report.ManagedRecipe,
		c.LexicalPath,
		c.ResolvedPath,
		c.IsSymlink,
	)
}

func validateManagedStoreBundleEntry(
	mr *managedRecipeStatus,
	lexicalPath, resolvedPath string,
	isSymlink bool,
) error {
	if err := validateManagedStoreLexicalEntry(mr, lexicalPath, isSymlink); err != nil {
		return err
	}
	return validateManagedStoreResolvedEntry(lexicalPath, resolvedPath)
}

func validateManagedStoreLexicalEntry(
	mr *managedRecipeStatus,
	lexicalPath string,
	isSymlink bool,
) error {
	if isSymlink {
		return fmt.Errorf(
			"managed_store candidate %q must not be a symlink (Addendum section 5)",
			lexicalPath,
		)
	}
	if mr == nil || strings.TrimSpace(mr.IllustrativeBundleRoot) == "" {
		return fmt.Errorf(
			"managed_store candidate %q requires managedRecipe.illustrativeBundleRoot",
			lexicalPath,
		)
	}
	if err := validateCleanAbsoluteSlashPath(lexicalPath, "managed_store lexicalPath"); err != nil {
		return err
	}
	root := strings.TrimSuffix(mr.IllustrativeBundleRoot, "/")
	prefix := root + "/"
	if !strings.HasPrefix(lexicalPath, prefix) {
		return fmt.Errorf(
			"managed_store candidate %q must reside directly under illustrativeBundleRoot %q",
			lexicalPath,
			mr.IllustrativeBundleRoot,
		)
	}
	rel := strings.TrimPrefix(lexicalPath, prefix)
	if err := validateFlatFilename(rel, "managed_store bundle-relative entry"); err != nil {
		return fmt.Errorf(
			"managed_store candidate %q must use flat bundle layout: %w",
			lexicalPath,
			err,
		)
	}
	if !slices.Contains(mr.ExpectedFiles, rel) {
		return fmt.Errorf(
			"managed_store bundle-relative entry %q is not listed in managedRecipe.expectedFiles %v",
			rel,
			mr.ExpectedFiles,
		)
	}
	return nil
}

func validateManagedStoreResolvedEntry(lexicalPath, resolvedPath string) error {
	if resolvedPath == "" {
		return nil
	}
	if err := validateCleanAbsoluteSlashPath(
		resolvedPath,
		"managed_store resolvedPath",
	); err != nil {
		return err
	}
	if resolvedPath != lexicalPath {
		return fmt.Errorf(
			"managed_store candidate %q must not resolve through a link to %q",
			lexicalPath,
			resolvedPath,
		)
	}
	return nil
}

func validateCleanAbsoluteSlashPath(p, field string) error {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return fmt.Errorf("%s %q must be a clean absolute slash path", field, p)
	}
	segments := strings.Split(p, "/")
	if slices.ContainsFunc(segments[1:], isInvalidPathSegment) {
		return fmt.Errorf(
			"%s %q must not contain empty, dot, or parent path segments",
			field,
			p,
		)
	}
	return nil
}

func isInvalidPathSegment(seg string) bool {
	return seg == "" || seg == "." || seg == ".."
}

func validateFlatFilename(name, field string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%s %q must not be dot or parent segment", field, name)
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("%s %q must be flat without subdirectory separators", field, name)
	}
	return nil
}

func validateAction(ctx targetContext, a *remediationAction) error {
	if err := validateActionHeaderAndAuth(a); err != nil {
		return err
	}
	if err := validateActionEnablement(ctx, a); err != nil {
		return err
	}
	return validateDisabledActionReason(ctx, a)
}

func validateActionHeaderAndAuth(a *remediationAction) error {
	if !slices.Contains(allActionKinds, a.Kind) {
		return fmt.Errorf("invalid action kind %q", a.Kind)
	}
	if strings.TrimSpace(a.Label) == "" {
		return fmt.Errorf("action label is required")
	}
	if isMutatingAction(a.Kind) && !a.RequiresEffectAuthorization {
		return fmt.Errorf("mutating action %q must set requiresEffectAuthorization=true", a.Kind)
	}
	if requiresUserConfirmation(a.Kind) && !a.RequiresConfirmation {
		return fmt.Errorf("action %q must set requiresConfirmation=true", a.Kind)
	}
	return nil
}

func isMutatingAction(k actionKind) bool {
	return k == actionInstall || k == actionRepair || k == actionSelect || k == actionRemove
}

func requiresUserConfirmation(k actionKind) bool {
	return k == actionInstall || k == actionSelect || k == actionRemove
}

func validateActionEnablement(ctx targetContext, a *remediationAction) error {
	if !a.Enabled {
		return nil
	}
	if err := validateEnabledActionTargetAndRecipe(ctx.report, a); err != nil {
		return err
	}
	return validateEnabledActionAdapterAndSelect(ctx, a)
}

func validateEnabledActionTargetAndRecipe(r *reportContract, a *remediationAction) error {
	if isStaleTarget(&r.Target) && isMutatingAction(a.Kind) {
		return fmt.Errorf("stale target report must not enable mutating action %q", a.Kind)
	}
	if a.Kind == actionInstall || a.Kind == actionRepair {
		return fmt.Errorf(
			"managed action %q cannot be enabled while all recipes remain unqualified",
			a.Kind,
		)
	}
	return nil
}

func validateEnabledActionAdapterAndSelect(ctx targetContext, a *remediationAction) error {
	r := ctx.report
	if ctx.isCanonicalMSB && !ctx.adapterSupportsT08 && isMutatingAction(a.Kind) {
		return fmt.Errorf(
			"mutating action %q cannot be enabled for MicroSandbox adapter %q lacking T08 support (G01)",
			a.Kind,
			r.Target.AdapterVersion,
		)
	}
	if isSelectBlockedByAvailability(r.Availability, a.Kind) {
		return fmt.Errorf("select action cannot be enabled when availability is %q", r.Availability)
	}
	return nil
}

func isStaleTarget(t *targetIdentity) bool {
	return t.StaleAgainstRevision != "" && t.StaleAgainstRevision != t.ProviderConfigRevision
}

func isSelectBlockedByAvailability(avail availabilityState, kind actionKind) bool {
	return kind == actionSelect && (avail == availabilityMissing || avail == availabilityIncomplete)
}

func validateDisabledActionReason(ctx targetContext, a *remediationAction) error {
	if a.Enabled {
		if a.DisabledReasonCode != "" {
			return fmt.Errorf(
				"enabled action %q must not specify disabledReasonCode %q",
				a.Kind,
				a.DisabledReasonCode,
			)
		}
		return nil
	}
	if a.DisabledReasonCode == "" {
		return fmt.Errorf("disabled action %q must specify disabledReasonCode", a.Kind)
	}
	if !slices.Contains(allReasonCodes, a.DisabledReasonCode) {
		return fmt.Errorf("invalid disabledReasonCode %q", a.DisabledReasonCode)
	}
	if a.DisabledReasonCode == reasonOfflineAssetMissing && !hasDisabledCanonicalMSBRecipe(ctx) {
		return fmt.Errorf(
			"disabledReasonCode=%q requires a canonical MicroSandbox target with a disabled managedRecipe",
			reasonOfflineAssetMissing,
		)
	}
	return nil
}

func buildPassiveAuthRequiredReport(t *testing.T, external *reportContract) *reportContract {
	t.Helper()
	r := cloneReport(t, external)
	r.SurveyMode = surveyModePassive
	r.Executed = false
	r.Compatibility = compatibilityUnknown
	r.Reachability = reachabilityNotChecked
	r.ReasonCode = reasonAuthRequired

	r.Items[0].Executed = false
	r.Items[0].Compatibility = compatibilityUnknown
	r.Items[0].Reachability = reachabilityNotChecked
	r.Items[0].ReasonCode = reasonAuthRequired

	r.Items[0].Candidates[0].Executed = false
	r.Items[0].Candidates[0].Compatibility = compatibilityUnknown
	r.Items[0].Candidates[0].ReasonCode = reasonAuthRequired
	r.Items[0].Candidates[0].Observation.ObservedVersion = ""
	return r
}

func buildRootAndRecipeDeletionCases(managedRaw []byte) []rawShapeCase {
	return []rawShapeCase{
		{
			name:   "missing root executed boolean",
			base:   managedRaw,
			mutate: func(doc map[string]any) { delete(doc, "executed") },
			errSub: `$: missing required field "executed"`,
		},
		{
			name: "missing target.trustedBuiltinIdentity boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(doc["target"].(map[string]any), "trustedBuiltinIdentity")
			},
			errSub: `$.target: missing required field "trustedBuiltinIdentity"`,
		},
		{
			name: "missing managedRecipe.enabled boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(doc["managedRecipe"].(map[string]any), "enabled")
			},
			errSub: `$.managedRecipe: missing required field "enabled"`,
		},
		{
			name: "missing managedRecipe.blockingGates array",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(doc["managedRecipe"].(map[string]any), "blockingGates")
			},
			errSub: `$.managedRecipe: missing required field "blockingGates"`,
		},
		{
			name: "missing item.required boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(firstItemMap(doc), "required")
			},
			errSub: `$.items[0]: missing required field "required"`,
		},
		{
			name: "missing item.executed boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(firstItemMap(doc), "executed")
			},
			errSub: `$.items[0]: missing required field "executed"`,
		},
	}
}

func buildItemAndActionDeletionCases(managedRaw, staleRaw []byte) []rawShapeCase {
	return []rawShapeCase{
		{
			name: "missing candidate.executed boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(firstCandidateMap(doc), "executed")
			},
			errSub: `$.items[0].candidates[0]: missing required field "executed"`,
		},
		{
			name: "missing candidate.selected boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(firstCandidateMap(doc), "selected")
			},
			errSub: `$.items[0].candidates[0]: missing required field "selected"`,
		},
		{
			name: "missing selectedPair.sourceKind",
			base: staleRaw,
			mutate: func(doc map[string]any) {
				pair := firstItemMap(doc)["selectedPair"].(map[string]any)
				delete(pair, "sourceKind")
			},
			errSub: `$.items[0].selectedPair: missing required field "sourceKind"`,
		},
		{
			name: "missing action.enabled boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(firstActionMap(doc), "enabled")
			},
			errSub: `$.actions[0]: missing required field "enabled"`,
		},
		{
			name: "missing action.requiresEffectAuthorization boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(firstActionMap(doc), "requiresEffectAuthorization")
			},
			errSub: `$.actions[0]: missing required field "requiresEffectAuthorization"`,
		},
		{
			name: "missing action.requiresConfirmation boolean",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				delete(firstActionMap(doc), "requiresConfirmation")
			},
			errSub: `$.actions[0]: missing required field "requiresConfirmation"`,
		},
	}
}

func buildTypeAndUnknownFieldCases(managedRaw []byte) []rawShapeCase {
	return []rawShapeCase{
		{
			name:   "wrong scalar type for root executed",
			base:   managedRaw,
			mutate: func(doc map[string]any) { doc["executed"] = "false" },
			errSub: `$.executed: expected boolean`,
		},
		{
			name:   "wrong scalar type for schemaVersion float",
			base:   managedRaw,
			mutate: func(doc map[string]any) { doc["schemaVersion"] = 1.5 },
			errSub: `$.schemaVersion: expected integer`,
		},
		{
			name: "wrong scalar type for candidate.selected",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				firstCandidateMap(doc)["selected"] = 0
			},
			errSub: `$.items[0].candidates[0].selected: expected boolean`,
		},
		{
			name:   "wrong container type for items",
			base:   managedRaw,
			mutate: func(doc map[string]any) { doc["items"] = map[string]any{} },
			errSub: `$.items: expected array`,
		},
		{
			name:   "unknown root field",
			base:   managedRaw,
			mutate: func(doc map[string]any) { doc["unexpectedRoot"] = true },
			errSub: `$: unknown field "unexpectedRoot"`,
		},
		{
			name: "unknown nested field in candidate",
			base: managedRaw,
			mutate: func(doc map[string]any) {
				firstCandidateMap(doc)["extraCandidateField"] = "nope"
			},
			errSub: `$.items[0].candidates[0]: unknown field "extraCandidateField"`,
		},
	}
}

func firstItemMap(doc map[string]any) map[string]any {
	return doc["items"].([]any)[0].(map[string]any)
}

func firstCandidateMap(doc map[string]any) map[string]any {
	return firstItemMap(doc)["candidates"].([]any)[0].(map[string]any)
}

func firstActionMap(doc map[string]any) map[string]any {
	return doc["actions"].([]any)[0].(map[string]any)
}

func buildNestedEnumMutationCases() []nestedCandidateMutationCase {
	return []nestedCandidateMutationCase{
		{
			name: "invalid selectedPair.sourceKind",
			mutate: func(r *reportContract) {
				r.Items[0].SelectedPair.SourceKind = candidateSourceKind("arbitrary_source")
			},
			errSub:          "invalid selectedPair.sourceKind",
			expectSchemaErr: true,
		},
		{
			name: "invalid candidate.availability",
			mutate: func(r *reportContract) {
				r.Items[0].Candidates[0].Availability = availabilityState("bogus_avail")
			},
			errSub:          "invalid candidate.availability",
			expectSchemaErr: true,
		},
		{
			name: "invalid candidate.compatibility",
			mutate: func(r *reportContract) {
				r.Items[0].Candidates[0].Compatibility = compatibilityState("bogus_compat")
			},
			errSub:          "invalid candidate.compatibility",
			expectSchemaErr: true,
		},
		{
			name: "invalid candidate.reasonCode",
			mutate: func(r *reportContract) {
				r.Items[0].Candidates[0].ReasonCode = reasonCode("dependency_bogus_reason")
			},
			errSub:          "invalid candidate.reasonCode",
			expectSchemaErr: true,
		},
	}
}

func buildMissingCandidateIdentityCases() []nestedCandidateMutationCase {
	return []nestedCandidateMutationCase{
		{
			name: "missing candidate.component",
			mutate: func(r *reportContract) {
				r.Items[0].Candidates[0].Component = ""
			},
			errSub:          "candidate.component is required",
			expectSchemaErr: true,
		},
		{
			name: "missing candidate.lexicalPath",
			mutate: func(r *reportContract) {
				r.Items[0].Candidates[0].LexicalPath = ""
			},
			errSub:          "candidate.lexicalPath is required",
			expectSchemaErr: true,
		},
		{
			name: "missing candidate.sourceKind",
			mutate: func(r *reportContract) {
				r.Items[0].Candidates[0].SourceKind = ""
			},
			errSub:          "invalid candidate.sourceKind",
			expectSchemaErr: true,
		},
		{
			name: "explicit_option candidate missing optionName rejected semantically",
			mutate: func(r *reportContract) {
				r.Items[0].Candidates[0].OptionName = ""
			},
			errSub:          "requires non-empty optionName",
			expectSchemaErr: false,
		},
	}
}

func cloneReport(t *testing.T, src *reportContract) *reportContract {
	t.Helper()
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var dst reportContract
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	return &dst
}

func toStrings[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

func buildUnsupportedSchemaKeywordCases() []schemaKeywordMutationCase {
	return []schemaKeywordMutationCase{
		{
			name:   "unsupported root validation keyword oneOf",
			mutate: func(doc map[string]any) { doc["oneOf"] = []any{} },
			errSub: `$: unsupported schema keyword "oneOf"`,
		},
		{
			name: "unsupported property validation keyword maxLength",
			mutate: func(doc map[string]any) {
				props := doc["properties"].(map[string]any)
				msg := props["message"].(map[string]any)
				msg["maxLength"] = 256
			},
			errSub: `$.properties.message: unsupported schema keyword "maxLength"`,
		},
		{
			name: "unsupported nested $defs validation keyword pattern",
			mutate: func(doc map[string]any) {
				defs := doc["$defs"].(map[string]any)
				ti := defs["targetIdentity"].(map[string]any)
				props := ti["properties"].(map[string]any)
				ctxName := props["contextName"].(map[string]any)
				ctxName["pattern"] = "^[a-z0-9-]+$"
			},
			errSub: `$.$defs.targetIdentity.properties.contextName: unsupported schema keyword "pattern"`,
		},
		{
			name: "unsupported array items validation keyword anyOf",
			mutate: func(doc map[string]any) {
				defs := doc["$defs"].(map[string]any)
				mr := defs["managedRecipeStatus"].(map[string]any)
				props := mr["properties"].(map[string]any)
				gates := props["blockingGates"].(map[string]any)
				items := gates["items"].(map[string]any)
				items["anyOf"] = []any{}
			},
			errSub: `$.$defs.managedRecipeStatus.properties.blockingGates.items: unsupported schema keyword "anyOf"`,
		},
	}
}

func buildIgnoredSchemaKeywordCombinationCases() []schemaKeywordMutationCase {
	return []schemaKeywordMutationCase{
		{
			name: "boolean property with const false rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "executed")["const"] = false
			},
			errSub: `$.properties.executed: keyword "const" is not supported on schema type "boolean"`,
		},
		{
			name: "$ref property with validation sibling minLength rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "availability")["minLength"] = 10
			},
			errSub: `$.properties.availability: $ref node cannot combine with validation keyword "minLength"`,
		},
		{
			name: "$ref property with validation sibling const rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "availability")["const"] = "available"
			},
			errSub: `$.properties.availability: $ref node cannot combine with validation keyword "const"`,
		},
		{
			name: "integer property with enum rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "schemaVersion")["enum"] = []string{"1"}
			},
			errSub: `$.properties.schemaVersion: keyword "enum" is not supported on schema type "integer"`,
		},
		{
			name: "boolean property with minLength rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "executed")["minLength"] = 1
			},
			errSub: `$.properties.executed: keyword "minLength" is not supported on schema type "boolean"`,
		},
		{
			name: "string property with minItems rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "message")["minItems"] = 1
			},
			errSub: `$.properties.message: keyword "minItems" is not supported on schema type "string"`,
		},
		{
			name: "string property with properties rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "message")["properties"] = map[string]any{}
			},
			errSub: `$.properties.message: keyword "properties" is not supported on schema type "string"`,
		},
	}
}

func buildInvalidSchemaConstraintValueCases() []schemaKeywordMutationCase {
	return []schemaKeywordMutationCase{
		{
			name: "string property with non-string const rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "schema")["const"] = false
			},
			errSub: `$.properties.schema.const: expected string const for type "string"`,
		},
		{
			name: "integer property with non-integer const rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "schemaVersion")["const"] = "1"
			},
			errSub: `$.properties.schemaVersion.const: expected integer const for type "integer"`,
		},
		{
			name: "integer property with fractional const rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "schemaVersion")["const"] = 1.5
			},
			errSub: `$.properties.schemaVersion.const: expected integer const for type "integer"`,
		},
		{
			name: "string property with empty enum rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "surveyMode")["enum"] = []string{}
			},
			errSub: `$.properties.surveyMode.enum: expected non-empty string array`,
		},
		{
			name: "string property with negative minLength rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "message")["minLength"] = -1
			},
			errSub: `$.properties.message.minLength: expected non-negative integer`,
		},
		{
			name: "array property with negative minItems rejected",
			mutate: func(doc map[string]any) {
				schemaRootPropertyMap(doc, "items")["minItems"] = -1
			},
			errSub: `$.properties.items.minItems: expected non-negative integer`,
		},
	}
}

func schemaRootPropertyMap(doc map[string]any, prop string) map[string]any {
	return doc["properties"].(map[string]any)[prop].(map[string]any)
}

func buildInexactIntegerSchemaLiteralCases() []schemaLiteralCase {
	const constErr = `$.const: expected integer const for type "integer"`
	return []schemaLiteralCase{
		{
			name:   "fractional const that rounds to 1 in float64 rejected",
			schema: `{"type":"integer","const":1.0000000000000001}`,
			errSub: constErr,
		},
		{
			name:   "exponent const rejected",
			schema: `{"type":"integer","const":1e0}`,
			errSub: constErr,
		},
		{
			name:   "const with zero fraction rejected",
			schema: `{"type":"integer","const":1.0}`,
			errSub: constErr,
		},
		{
			name:   "const beyond int64 rejected",
			schema: `{"type":"integer","const":9223372036854775808}`,
			errSub: constErr,
		},
		{
			name:   "fractional minLength that rounds to 1 in float64 rejected",
			schema: `{"type":"string","minLength":1.0000000000000001}`,
			errSub: `$.minLength: expected non-negative integer`,
		},
		{
			name:   "exponent minItems rejected",
			schema: `{"type":"array","minItems":1e0}`,
			errSub: `$.minItems: expected non-negative integer`,
		},
		{
			name:   "negative minLength literal rejected",
			schema: `{"type":"string","minLength":-1}`,
			errSub: `$.minLength: expected non-negative integer`,
		},
	}
}

func buildExactIntegerValidationLiteralCases() []schemaLiteralCase {
	const beyondFloat64 = `{"type":"integer","const":9007199254740993}`
	const ordinaryInteger = `{"type":"integer","const":1}`
	return []schemaLiteralCase{
		{
			name:   "const beyond float64 precision rejects adjacent integer",
			schema: beyondFloat64,
			input:  `9007199254740992`,
			errSub: `$: expected const 9007199254740993, got 9007199254740992`,
		},
		{
			name:   "const beyond float64 precision accepts exact integer",
			schema: beyondFloat64,
			input:  `9007199254740993`,
		},
		{
			name:   "ordinary integer const accepts match",
			schema: ordinaryInteger,
			input:  `1`,
		},
		{
			name:   "ordinary integer const rejects mismatch",
			schema: ordinaryInteger,
			input:  `2`,
			errSub: `$: expected const 1, got 2`,
		},
		{
			name:   "ordinary integer const rejects fractional input",
			schema: ordinaryInteger,
			input:  `1.0`,
			errSub: `$: expected integer`,
		},
		{
			name:   "integer input beyond int64 rejected",
			schema: `{"type":"integer"}`,
			input:  `9223372036854775808`,
			errSub: `$: invalid integer 9223372036854775808`,
		},
	}
}

func buildUnicodeMinLengthLiteralCases() []schemaLiteralCase {
	const minTwo = `{"type":"string","minLength":2}`
	const belowMinTwo = `$: string length 1 is below minLength 2`
	return []schemaLiteralCase{
		{
			name:   "one two-byte character is below minLength 2",
			schema: minTwo,
			input:  `"\u00e9"`,
			errSub: belowMinTwo,
		},
		{
			name:   "one supplementary-plane character is below minLength 2",
			schema: minTwo,
			input:  `"\ud83d\ude00"`,
			errSub: belowMinTwo,
		},
		{
			name:   "two accented characters satisfy minLength 2",
			schema: minTwo,
			input:  `"\u00e9\u00e8"`,
		},
		{
			name:   "two ASCII characters satisfy minLength 2",
			schema: minTwo,
			input:  `"ab"`,
		},
		{
			name:   "one ASCII character is below minLength 2",
			schema: minTwo,
			input:  `"a"`,
			errSub: belowMinTwo,
		},
		{
			name:   "empty string satisfies minLength 0",
			schema: `{"type":"string","minLength":0}`,
			input:  `""`,
		},
	}
}
