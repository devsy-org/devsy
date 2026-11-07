package dependencies

import "time"

// PassiveReportMetadata supplies facts that passive lookup cannot establish.
// Redact must be deterministic and side-effect-free. VirtualizationCapability
// must be an explicit caller assertion, including when it is not_required.
// Revisions in SurveyTarget identify snapshots, not files or CAS authority.
type PassiveReportMetadata struct {
	EvaluatedAt              time.Time
	OSVersion                string
	VirtualizationCapability string
	Redact                   func(string) string
}

// Report is the bounded passive subset of the frozen v1 wire contract.
// Its mutable values confer no execution, selection, or installation authority.
type Report struct {
	Schema        string         `json:"schema"`
	SchemaVersion int            `json:"schemaVersion"`
	SurveyMode    string         `json:"surveyMode"`
	Executed      bool           `json:"executed"`
	Target        ReportTarget   `json:"target"`
	Host          ReportHost     `json:"host"`
	Availability  string         `json:"availability"`
	Compatibility string         `json:"compatibility"`
	Reachability  string         `json:"reachability"`
	Policy        string         `json:"policy"`
	ReasonCode    string         `json:"reasonCode"`
	Message       string         `json:"message"`
	Items         []ReportItem   `json:"items"`
	Actions       []ReportAction `json:"actions"`
}

type ReportTarget struct {
	ContextName            string `json:"contextName"`
	ProviderName           string `json:"providerName"`
	ProviderType           string `json:"providerType"`
	ProviderSourceID       string `json:"providerSourceId"`
	ProviderSourceKind     string `json:"providerSourceKind"`
	TrustedBuiltinIdentity bool   `json:"trustedBuiltinIdentity"`
	ConfigRevision         string `json:"configRevision"`
	ProviderConfigRevision string `json:"providerConfigRevision"`
	EvaluatedAt            string `json:"evaluatedAt"`
}

type ReportHost struct {
	GOOS                     string `json:"goos"`
	GOARCH                   string `json:"goarch"`
	OSVersion                string `json:"osVersion,omitempty"`
	VirtualizationCapability string `json:"virtualizationCapability"`
}

type ReportItem struct {
	ID            string            `json:"id"`
	DisplayName   string            `json:"displayName"`
	Required      bool              `json:"required"`
	Executed      bool              `json:"executed"`
	Availability  string            `json:"availability"`
	Compatibility string            `json:"compatibility"`
	Reachability  string            `json:"reachability"`
	Policy        string            `json:"policy"`
	ReasonCode    string            `json:"reasonCode"`
	Candidates    []ReportCandidate `json:"candidates"`
}

// ReportCandidate intentionally has no observation or native identity input seam.
type ReportCandidate struct {
	Component     string `json:"component"`
	SourceKind    string `json:"sourceKind"`
	OptionName    string `json:"optionName,omitempty"`
	LexicalPath   string `json:"lexicalPath"`
	ResolvedPath  string `json:"resolvedPath"`
	IsSymlink     bool   `json:"isSymlink,omitempty"`
	Executed      bool   `json:"executed"`
	Selected      bool   `json:"selected"`
	Availability  string `json:"availability"`
	Compatibility string `json:"compatibility"`
	ReasonCode    string `json:"reasonCode"`
	Detail        string `json:"detail,omitempty"`
}

// ReportAction describes the frozen action shape. Passive conversion emits none.
type ReportAction struct {
	Kind                        string `json:"kind"`
	Label                       string `json:"label"`
	Enabled                     bool   `json:"enabled"`
	DisabledReasonCode          string `json:"disabledReasonCode,omitempty"`
	RequiresEffectAuthorization bool   `json:"requiresEffectAuthorization"`
	RequiresConfirmation        bool   `json:"requiresConfirmation"`
	CLICommand                  string `json:"cliCommand,omitempty"`
}
