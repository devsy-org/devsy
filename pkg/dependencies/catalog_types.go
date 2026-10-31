package dependencies

type SourceKind string

const (
	toolDocker            = "docker"
	toolMicrosandbox      = "microsandbox"
	toolApple             = "apple"
	toolLima              = "lima"
	toolOrbStack          = "orbstack"
	executionDocker       = "internal_docker"
	platformLinuxAMD64    = "linux/amd64"
	platformDarwinARM64   = "darwin/arm64"
	imageBackendNone      = "none"
	executionMicrosandbox = "external_microsandbox_v1"
)

const (
	SourceInternal SourceKind = "internal"
	SourceGitHub   SourceKind = "github"
	SourceURL      SourceKind = "url"
	SourceFile     SourceKind = "file"
)

type ProviderSource struct {
	Kind       SourceKind `json:"kind"`
	InternalID string     `json:"internalId,omitempty"`
	Repository string     `json:"repository,omitempty"`
	Location   string     `json:"location,omitempty"`
}

// ProviderContract facts must come from trusted orchestration inspecting source and full manifest, not display names.
type ProviderContract struct {
	DisplayName        string
	Source             ProviderSource
	ExecutionContract  string
	AdapterVersion     string
	ManifestSHA256     string
	RuntimeCheckSchema int
	ImageBackend       string
}

type Requirement struct {
	ToolID              string   `json:"toolId"`
	Purpose             string   `json:"purpose"`
	Scope               string   `json:"scope"`
	RequiredFor         []string `json:"requiredFor"`
	ExecutableOption    string   `json:"executableOption,omitempty"`
	ConfigurationOption string   `json:"configurationOption,omitempty"`
	ProbeID             string   `json:"probeId,omitempty"`
	GuidanceID          string   `json:"guidanceId"`
	WhenImageBackend    string   `json:"whenImageBackend,omitempty"`
}

type Binding struct {
	ID                         string         `json:"id"`
	Source                     ProviderSource `json:"source"`
	ExecutionContract          string         `json:"executionContract"`
	AdapterSemverRange         string         `json:"adapterSemverRange"`
	RequiredRuntimeCheckSchema int            `json:"requiredRuntimeCheckSchema"`
	// Platforms describes external discovery, never managed qualification.
	Platforms    []string      `json:"platforms"`
	GuidanceID   string        `json:"guidanceId"`
	Requirements []Requirement `json:"requirements"`
}

type ContractMatch struct {
	BindingID        string
	GuidanceID       string
	ContractVerified bool
	AdapterStatus    string
	ManagedEligible  bool
	ManagedReason    string
	Requirements     []Requirement
}

type ArchiveMember struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	MaxBytes int64  `json:"maxBytes"`
}

type Recipe struct {
	ID                  string          `json:"id"`
	ToolID              string          `json:"toolId"`
	RuntimeVersion      string          `json:"runtimeVersion"`
	Platform            string          `json:"platform"`
	URL                 string          `json:"url"`
	SHA256              string          `json:"sha256"`
	ArchiveFormat       string          `json:"archiveFormat"`
	Layout              string          `json:"layout"`
	MaxArchiveBytes     int64           `json:"maxArchiveBytes"`
	MaxExpandedBytes    int64           `json:"maxExpandedBytes"`
	Members             []ArchiveMember `json:"members"`
	Executable          string          `json:"executable"`
	Firmware            string          `json:"firmware"`
	Prerequisites       []string        `json:"prerequisites"`
	ProbeID             string          `json:"probeId"`
	Actions             []string        `json:"actions"`
	QualificationStatus string          `json:"qualificationStatus"`
	QualificationRecord string          `json:"qualificationRecord"`
	Enabled             bool            `json:"enabled"`
}
