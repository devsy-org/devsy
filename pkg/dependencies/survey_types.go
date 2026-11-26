package dependencies

// SurveyOption is an already-effective static executable option. Map presence
// distinguishes absent from empty. Origin is default, explicit_option, or
// explicit_env; an empty origin means default. UserProvided preserves explicit
// intent even when the value equals the reviewed default command.
type SurveyOption struct {
	Value        string
	Origin       LookupOrigin
	UserProvided bool
}

const SurveyDefault LookupOrigin = "default"

// SurveyTarget contains opaque orchestration identities, never credentials or
// raw source URLs. Consumers must compare all identities before using a result.
type SurveyTarget struct {
	StoreID                string
	ContextName            string
	ProviderName           string
	ProviderFingerprint    string
	ConfigRevision         string
	ProviderConfigRevision string
}

// SurveyHost contains caller-observed facts. Empty fields remain unknown.
// GOOS and GOARCH describe the process performing lookup, not a remote host.
type SurveyHost struct {
	GOOS                     string
	GOARCH                   string
	NativeArchitecture       string
	VirtualizationCapability string
}

// SurveyRequest has no runner, dynamic option resolver, store, or active probe.
// Contract must be established by trusted orchestration; source names alone do
// not establish its assertions. Callers must not mutate inputs during Survey.
type SurveyRequest struct {
	Target      SurveyTarget
	Host        SurveyHost
	Contract    ProviderContract
	Environment LookupEnvironment
	Options     map[string]SurveyOption
	// ReviewedLocations contains complete trusted paths, keyed by matched ToolID.
	// Entries for tools outside the matched requirements are ignored.
	ReviewedLocations map[string][]string
}

// SurveyResult is an internal passive observation, not the v1 CLI wire report.
// It contains no saved selection, pair certification, or execution authority.
type SurveyResult struct {
	Target              SurveyTarget
	Host                SurveyHost
	CatalogID           string
	CatalogHash         string
	BindingID           string
	ContractVerified    bool
	AdapterStatus       string
	PolicyReason        string
	ManagedCapabilities SurveyManagedCapabilities
	Requirements        []RequirementSurvey
	Executed            bool
}

// SurveyManagedCapabilities remains all false in passive Survey.
type SurveyManagedCapabilities struct {
	Install bool
	Select  bool
	Remove  bool
	Update  bool
}

type RequirementSurvey struct {
	Requirement   Requirement
	Availability  string
	Compatibility string
	Reachability  string
	Policy        string
	PolicyReason  string
	ReasonCode    string
	Candidates    []ExecutableCandidate
	Executed      bool
}
