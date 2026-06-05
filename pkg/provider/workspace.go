package provider

import (
	"net/url"
	"strings"
	"time"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/config"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/util"
)

var (
	WorkspaceSourceGit       = "git:"
	WorkspaceSourceLocal     = "local:"
	WorkspaceSourceImage     = "image:"
	WorkspaceSourceContainer = "container:"
	WorkspaceSourceUnknown   = "unknown:"
)

type Workspace struct {
	// ID is the workspace id to use
	ID string `json:"id,omitempty"`

	// UID is used to identify this specific workspace
	UID string `json:"uid,omitempty"`

	// Picture is the project social media image
	Picture string `json:"picture,omitempty"`

	// Provider is the provider used to create this workspace
	Provider WorkspaceProviderConfig `json:"provider"`

	// Machine is the machine to use for this workspace
	Machine WorkspaceMachineConfig `json:"machine"`

	// IDE holds IDE specific settings
	IDE WorkspaceIDEConfig `json:"ide"`

	// Source is the source where this workspace will be created from
	Source WorkspaceSource `json:"source"`

	// DevContainerImage is the container image to use, overriding whatever is in the devcontainer.json
	DevContainerImage string `json:"devContainerImage,omitempty"`

	// DevContainerPath is the relative path where the devcontainer.json is located.
	DevContainerPath string `json:"devContainerPath,omitempty"`

	// DevContainerConfig holds the config for the devcontainer.json.
	DevContainerConfig *devcontainerconfig.DevContainerConfig `json:"devContainerConfig,omitempty"`

	// CreationTimestamp is the timestamp when this workspace was created
	CreationTimestamp types.Time `json:"creationTimestamp"`

	// LastUsedTimestamp holds the timestamp when this workspace was last accessed
	LastUsedTimestamp types.Time `json:"lastUsed"`

	// Context is the context where this config file was loaded from
	Context string `json:"context,omitempty"`

	// Imported signals that this workspace was imported
	Imported bool `json:"imported,omitempty"`

	// Origin is the place where this config file was loaded from
	Origin string `json:"-"`

	// Pro signals this workspace is remote and doesn't necessarily exist locally. It also has more metadata about the pro workspace
	Pro *ProMetadata `json:"pro,omitempty"`

	// Path to the file where the SSH config to access the workspace is stored
	SSHConfigPath string `json:"sshConfigPath,omitempty"`

	// Path to an alternate file where Devsy entries are written (for read-only SSH configs)
	SSHConfigIncludePath string `json:"sshConfigIncludePath,omitempty"`
}

type ProMetadata struct {
	// InstanceName is the platform CRD name for this workspace
	InstanceName string `json:"instanceName,omitempty"`

	// Project is the platform project the workspace lives in
	Project string `json:"project,omitempty"`

	// DisplayName is the name intended to show users
	DisplayName string `json:"displayName,omitempty"`
}

type WorkspaceIDEConfig struct {
	// Name is the name of the IDE
	Name string `json:"name,omitempty"`

	// Options are the local options that override the global ones
	Options map[string]config.OptionValue `json:"options,omitempty"`
}

type WorkspaceMachineConfig struct {
	// ID is the machine ID to use for this workspace
	ID string `json:"machineId,omitempty"`

	// AutoDelete specifies if the machine should get destroyed when
	// the workspace is destroyed
	AutoDelete bool `json:"autoDelete,omitempty"`
}

type WorkspaceProviderConfig struct {
	// Name is the provider name
	Name string `json:"name,omitempty"`

	// Options are the local options that override the global ones
	Options map[string]config.OptionValue `json:"options,omitempty"`
}

type WorkspaceSource struct {
	// GitRepository is the repository to clone
	GitRepository string `json:"gitRepository,omitempty"`

	// GitBranch is the branch to use
	GitBranch string `json:"gitBranch,omitempty"`

	// GitCommit is the commit SHA to checkout
	GitCommit string `json:"gitCommit,omitempty"`

	// GitPRReference is the pull request reference to checkout
	GitPRReference string `json:"gitPRReference,omitempty"`

	// GitSubPath is the subpath in the repo to use
	GitSubPath string `json:"gitSubDir,omitempty"`

	// LocalFolder is the local folder to use
	LocalFolder string `json:"localFolder,omitempty"`

	// Image is the docker image to use
	Image string `json:"image,omitempty"`

	// Container is the container to use
	Container string `json:"container,omitempty"`
}

type ContainerWorkspaceInfo struct {
	// IDE holds the ide config options
	IDE WorkspaceIDEConfig `json:"ide"`

	// CLIOptions holds the cli options
	CLIOptions CLIOptions `json:"cliOptions"`

	// Dockerless holds custom dockerless configuration
	Dockerless ProviderDockerlessOptions `json:"dockerless"`

	// ContainerTimeout is the timeout in minutes to wait until the agent tries
	// to delete the container.
	ContainerTimeout string `json:"containerInactivityTimeout,omitempty"`

	// Source is a WorkspaceSource to be used inside the container
	Source WorkspaceSource `json:"source"`

	// ContentFolder holds the folder where the content is stored
	ContentFolder string `json:"contentFolder,omitempty"`

	// PullFromInsideContainer determines if project should be pulled from Source when container starts
	PullFromInsideContainer types.StrBool `json:"pullFromInsideContainer,omitempty"`

	// Agent holds the agent info
	Agent ProviderAgentConfig `json:"agent"`
}

type AgentWorkspaceInfo struct {
	// WorkspaceOrigin is the path where this workspace config originated from
	WorkspaceOrigin string `json:"workspaceOrigin,omitempty"`

	// Workspace holds the workspace info
	Workspace *Workspace `json:"workspace,omitempty"`

	// LastDevContainerConfig can be used as a fallback if the workspace was already started
	// and we lost track of the devcontainer.json
	LastDevContainerConfig *devcontainerconfig.DevContainerConfigWithPath `json:"lastDevContainerConfig,omitempty"`

	// Machine holds the machine info
	Machine *Machine `json:"machine,omitempty"`

	// Agent holds the agent info
	Agent ProviderAgentConfig `json:"agent"`

	// CLIOptions holds the cli options
	CLIOptions CLIOptions `json:"cliOptions"`

	// Options holds the filled provider options for this workspace
	Options map[string]config.OptionValue `json:"options,omitempty"`

	// ContentFolder holds the folder where the content is stored
	ContentFolder string `json:"contentFolder,omitempty"`

	// Origin holds the folder where this config was loaded from
	Origin string `json:"-"`

	// InjectTimeout specifies how long to wait for the agent to be injected into the dev container
	InjectTimeout time.Duration `json:"injectTimeout,omitempty"`

	// RegistryCache defines the registry to use for caching builds
	RegistryCache string `json:"registryCache,omitempty"`
}

type CLIOptions struct {
	// Platform are the platform options
	Platform devsy.PlatformOptions `json:"platformOptions"`

	// up options
	ID                          string            `json:"id,omitempty"`
	Source                      string            `json:"source,omitempty"`
	IDE                         string            `json:"ide,omitempty"`
	IDEOptions                  []string          `json:"ideOptions,omitempty"`
	PrebuildRepositories        []string          `json:"prebuildRepositories,omitempty"`
	DevContainerImage           string            `json:"devContainerImage,omitempty"`
	DevContainerPath            string            `json:"devContainerPath,omitempty"`
	DevContainerID              string            `json:"devContainerID,omitempty"`
	WorkspaceEnv                []string          `json:"workspaceEnv,omitempty"`
	WorkspaceEnvFile            []string          `json:"workspaceEnvFile,omitempty"`
	SecretsEnv                  []string          `json:"secretsEnv,omitempty"`
	FeatureSecretsFile          string            `json:"featureSecretsFile,omitempty"`
	InitEnv                     []string          `json:"initEnv,omitempty"`
	Recreate                    bool              `json:"recreate,omitempty"`
	Prebuild                    bool              `json:"prebuild,omitempty"`
	Reset                       bool              `json:"reset,omitempty"`
	DisableDaemon               bool              `json:"disableDaemon,omitempty"`
	DaemonInterval              string            `json:"daemonInterval,omitempty"`
	GitCloneStrategy            git.CloneStrategy `json:"gitCloneStrategy,omitempty"`
	GitCloneRecursiveSubmodules bool              `json:"gitCloneRecursive,omitempty"`
	FallbackImage               string            `json:"fallbackImage,omitempty"`
	GitSSHSigningKey            string            `json:"gitSshSigningKey,omitempty"`
	SSHAuthSockID               string            `json:"sshAuthSockID,omitempty"` // ID to use when looking for SSH_AUTH_SOCK, defaults to a new random ID if not set (only used for browser IDEs)
	StrictHostKeyChecking       bool              `json:"strictHostKeyChecking,omitempty"`
	AdditionalFeatures          string            `json:"additionalFeatures,omitempty"`
	ExtraDevContainerPath       string            `json:"extraDevContainerPath,omitempty"`
	User                        string            `json:"user,omitempty"`
	DefaultUserEnvProbe         string            `json:"defaultUserEnvProbe,omitempty"`
	Userns                      string            `json:"userns,omitempty"`
	UidMap                      []string          `json:"uidMap,omitempty"`
	GidMap                      []string          `json:"gidMap,omitempty"`
	IDLabels                    []string          `json:"idLabels,omitempty"`
	GPUAvailability             string            `json:"gpuAvailability,omitempty"`
	WorkspaceMountConsistency   string            `json:"workspaceMountConsistency,omitempty"`
	Mounts                      []string          `json:"mounts,omitempty"`
	UpdateRemoteUserUIDDefault  string            `json:"updateRemoteUserUIDDefault,omitempty"`
	ContainerDataFolder         string            `json:"containerDataFolder,omitempty"`
	MountWorkspaceGitRoot       *bool             `json:"mountWorkspaceGitRoot,omitempty"`
	TerminalColumns             int               `json:"terminalColumns,omitempty"`
	TerminalRows                int               `json:"terminalRows,omitempty"`
	SkipNonBlockingCommands     bool              `json:"skipNonBlockingCommands,omitempty"`
	ContainerUser               string            `json:"containerUser,omitempty"`
	RemoteUser                  string            `json:"remoteUser,omitempty"`

	// skip lifecycle hook options
	SkipPostCreate       bool `json:"skipPostCreate,omitempty"`
	SkipPostStart        bool `json:"skipPostStart,omitempty"`
	SkipPostAttach       bool `json:"skipPostAttach,omitempty"`
	SkipHostRequirements bool `json:"skipHostRequirements,omitempty"`

	// dotfiles options
	DotfilesRepo       string `json:"dotfilesRepo,omitempty"`
	DotfilesScript     string `json:"dotfilesScript,omitempty"`
	DotfilesTargetPath string `json:"dotfilesTargetPath,omitempty"`

	// build options
	// Repository specifies the container registry repository to push the built image to (e.g., ghcr.io/user/image).
	// When set, the image will be tagged and pushed to this repository after building.
	Repository string `json:"repository,omitempty"`
	// SkipPush prevents pushing the built image to the repository. Useful for testing builds
	// without affecting the registry. When true, the image is only built and loaded locally.
	SkipPush bool `json:"skipPush,omitempty"`
	// PushDuringBuild pushes the image directly to the registry during the build process,
	// skipping the load-to-daemon step. This is an optimization for CI/CD workflows. When true,
	// the build uses BuildKit's direct push capability (--push flag) instead of the default
	// load behavior (--load flag). Requires Repository to be set and cannot be
	// used with SkipPush.
	PushDuringBuild bool `json:"pushDuringBuild,omitempty"`
	// Platforms specifies the target platforms for multi-architecture builds (e.g., linux/amd64,linux/arm64).
	Platforms []string `json:"platform,omitempty"`
	// Tag specifies additional image tags to apply to the built image beyond the default prebuild hash tag.
	Tag []string `json:"tag,omitempty"`
	// CacheFrom specifies images to use as cache sources. When set, these take priority over
	// devcontainer.json build.cacheFrom values.
	CacheFrom            []string `json:"cacheFrom,omitempty"`
	NoCache              bool     `json:"noCache,omitempty"`
	Labels               []string `json:"labels,omitempty"`
	Output               string   `json:"output,omitempty"`
	ExperimentalLockfile string   `json:"experimentalLockfile,omitempty"`
	// ImageName specifies an alternative name for the built image.
	ImageName string `json:"imageName,omitempty"`
	// NoBuild prevents building; the command will fail if the image does not exist.
	NoBuild bool `json:"noBuild,omitempty"`

	// ForceBuild forces a rebuild even if a cached image exists.
	ForceBuild bool `json:"forceBuild,omitempty"`
	// ForceDockerless forces the use of a dockerless build approach.
	ForceDockerless bool `json:"forceDockerless,omitempty"`
	// ForceInternalBuildKit forces the use of internal BuildKit instead of docker buildx.
	ForceInternalBuildKit bool `json:"forceInternalBuildKit,omitempty"`
}

// BuildOptions extends CLIOptions with additional build-specific configuration.
type BuildOptions struct {
	CLIOptions

	// Platform specifies the target platform for the build (e.g., linux/amd64).
	Platform string
	// RegistryCache specifies a registry location to use for build cache storage and retrieval.
	// When set, BuildKit will use type=registry cache with this reference.
	RegistryCache string
	// ExportCache controls whether to export the build cache to the registry.
	// Only applies when RegistryCache is set.
	ExportCache bool
	// NoBuild prevents building the container image. When true, the command will fail if the image
	// does not already exist. Used to enforce that images must be pre-built.
	NoBuild bool
	// PushDuringBuild enables pushing the image directly to the registry during the build process,
	// bypassing the load-to-daemon step. This improves build performance in CI/CD
	// environments by avoiding the tar export/import overhead. When enabled, the image is pushed
	// directly from BuildKit to the registry without being loaded into the local Docker daemon.
	// This requires a repository to be specified and is mutually exclusive with SkipPush.
	PushDuringBuild bool
}

func (w WorkspaceSource) String() string {
	if w.GitRepository != "" {
		if w.GitPRReference != "" {
			return WorkspaceSourceGit + w.GitRepository + "@" + w.GitPRReference
		} else if w.GitBranch != "" {
			return WorkspaceSourceGit + w.GitRepository + "@" + w.GitBranch
		} else if w.GitCommit != "" {
			return WorkspaceSourceGit + w.GitRepository + git.CommitDelimiter + w.GitCommit
		}

		return WorkspaceSourceGit + w.GitRepository
	} else if w.LocalFolder != "" {
		return WorkspaceSourceLocal + w.LocalFolder
	} else if w.Image != "" {
		return WorkspaceSourceImage + w.Image
	} else if w.Container != "" {
		return WorkspaceSourceContainer + w.Container
	}

	return ""
}

func (w WorkspaceSource) Type() string {
	if w.GitRepository != "" {
		if w.GitPRReference != "" {
			return WorkspaceSourceGit + "pr"
		} else if w.GitBranch != "" {
			return WorkspaceSourceGit + "branch"
		} else if w.GitCommit != "" {
			return WorkspaceSourceGit + "commit"
		}

		return WorkspaceSourceGit
	} else if w.LocalFolder != "" {
		return WorkspaceSourceLocal
	} else if w.Image != "" {
		return WorkspaceSourceImage
	} else if w.Container != "" {
		return WorkspaceSourceContainer
	}

	return WorkspaceSourceUnknown
}

func ParseWorkspaceSource(source string) *WorkspaceSource {
	if after, ok := strings.CutPrefix(source, WorkspaceSourceGit); ok {
		gitRepo, gitPRReference, gitBranch, gitCommit, gitSubdir := git.NormalizeRepository(after)
		if !isPlausibleGitSource(gitRepo) {
			return nil
		}
		return &WorkspaceSource{
			GitRepository:  gitRepo,
			GitPRReference: gitPRReference,
			GitBranch:      gitBranch,
			GitCommit:      gitCommit,
			GitSubPath:     gitSubdir,
		}
	} else if after, ok := strings.CutPrefix(source, WorkspaceSourceLocal); ok {
		after = util.ExpandTilde(after)
		return &WorkspaceSource{
			LocalFolder: after,
		}
	} else if after, ok := strings.CutPrefix(source, WorkspaceSourceImage); ok {
		return &WorkspaceSource{
			Image: after,
		}
	} else if after, ok := strings.CutPrefix(source, WorkspaceSourceContainer); ok {
		return &WorkspaceSource{
			Container: after,
		}
	}

	return nil
}

var gitURLSchemes = map[string]bool{"http": true, "https": true, "ssh": true, "git": true}

// isPlausibleGitSource returns true when s looks like a git repository
// reference. Accepts HTTP(S)/SSH/file URLs and the scp-like "git@host:path"
// shape; rejects empty strings and obvious garbage so callers fail early
// instead of constructing a clone URL that git itself rejects with a
// confusing parser error.
func isPlausibleGitSource(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "git@") {
		return strings.Contains(s, ":")
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme == "file" {
		return u.Path != ""
	}
	if !gitURLSchemes[u.Scheme] || u.Host == "" {
		return false
	}
	// Catch nested schemes like "https://git:https://host/repo" — Host would
	// be "git" and a real port would be missing.
	return !strings.Contains(u.Host, ":") || u.Port() != ""
}

func (w *Workspace) IsPro() bool {
	return w.Pro != nil
}
