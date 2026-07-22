// Package names is the single source of truth for devsy CLI flag names.
//
// Flags are registered in cmd/ but many are also used to re-invoke devsy from
// pkg/ (e.g. building a "devsy workspace ssh ..." command line). This package
// is a dependency-light leaf (imports only fmt) so both the cobra-coupled
// pkg/flags builder and the pkg/ command builders can import it without
// pulling in cobra. Defining each name once means a rename is a single edit the
// compiler propagates to every registration and construction site.
//
// Names are the bare flag name (no leading "--"). Use Flag or FlagTrue/FlagFalse
// to build a command-line token.
//
// Every name here is a devsy-owned CLI flag and can be renamed by editing its
// value once. Some names deliberately mirror an external tool or spec — docker
// build (--pull, --no-cache, --platform), podman (--userns, --uidmap, --gidmap),
// git (--git-recurse-submodules), or the devcontainer.json schema (--features,
// --user-env-probe, --update-remote-user-uid, --id-label). Renaming those is a
// UX/compatibility decision, not a free change. (The literal flags passed
// through to docker/podman themselves live at their construction sites, e.g.
// pkg/driver/docker, not here.)
package names

import "fmt"

// Flag renders a flag name as a command-line token, e.g. Flag(SSHGPGForwarding)
// -> "--ssh-gpg-forwarding".
func Flag(name string) string { return "--" + name }

// FlagValue renders "--name=value".
func FlagValue(name, value string) string { return fmt.Sprintf("--%s=%s", name, value) }

// FlagTrue renders "--name=true".
func FlagTrue(name string) string { return FlagValue(name, "true") }

// FlagFalse renders "--name=false".
func FlagFalse(name string) string { return FlagValue(name, "false") }

// Workspace up / build / exec — devcontainer source & modifiers.
const (
	DevContainer          = "devcontainer"
	DevContainerImage     = "devcontainer-image"
	DevContainerPath      = "devcontainer-path"
	DevContainerID        = "devcontainer-id"
	DevContainerOverlay   = "devcontainer-overlay"
	FallbackImage         = "fallback-image"
	Features              = "features"
	IDLabel               = "id-label"
	UserEnvProbe          = "user-env-probe"
	GPUAvailability       = "gpu-availability"
	UpdateRemoteUserUID   = "update-remote-user-uid"
	ContainerDataFolder   = "container-data-folder"
	MountWorkspaceGitRoot = "mount-workspace-git-root"
	ContainerUser         = "container-user"
	RemoteUser            = "remote-user"
)

// SSH.
const (
	SSHConfigure         = "ssh-configure"
	SSHGPGForwarding     = "ssh-gpg-forwarding"
	SSHConfig            = "ssh-config"
	SSHTunnel            = "ssh-tunnel"
	AgentForwarding      = "agent-forwarding"
	ReuseSSHAuthSock     = "reuse-ssh-auth-sock"
	Stdio                = "stdio"
	StartServices        = "start-services"
	SSHKeepAliveInterval = "ssh-keepalive-interval"
	ForwardPorts         = "forward-ports"
	ReverseForwardPorts  = "reverse-forward-ports"
	SendEnv              = "send-env"
	SetEnv               = "set-env"
	ForwardPortsTimeout  = "forward-ports-timeout"
	TermMode             = "term-mode"
	InstallTerminfo      = "install-terminfo"
	TrackActivity        = "track-activity"
)

// Dotfiles.
const (
	Dotfiles              = "dotfiles"
	DotfilesScript        = "dotfiles-script"
	DotfilesTargetPath    = "dotfiles-target-path"
	DotfilesScriptEnv     = "dotfiles-script-env"
	DotfilesScriptEnvFile = "dotfiles-script-env-file"
	DotfilesRepo          = "dotfiles-repo"
)

// Git.
const (
	GitCloneStrategy     = "git-clone-strategy"
	GitRecurseSubmodules = "git-recurse-submodules"
	GitLFSMode           = "git-lfs-mode"
	GitSSHSigningKey     = "git-ssh-signing-key"
)

// Lifecycle.
const (
	SkipPostCreate          = "skip-post-create"
	SkipPostStart           = "skip-post-start"
	SkipPostAttach          = "skip-post-attach"
	SkipNonBlockingCommands = "skip-non-blocking-commands"
	SkipHostRequirements    = "skip-host-requirements"
	TerminalColumns         = "terminal-columns"
	TerminalRows            = "terminal-rows"
)

// Workspace behavior & identity.
const (
	ID                        = "id"
	Machine                   = "machine"
	Source                    = "source"
	ProviderOption            = "provider-option"
	Reconfigure               = "reconfigure"
	Prebuild                  = "prebuild"
	Pull                      = "pull"
	NoCache                   = "no-cache"
	Recreate                  = "recreate"
	Reset                     = "reset"
	PrebuildRepo              = "prebuild-repo"
	WorkspaceEnv              = "workspace-env"
	WorkspaceEnvFile          = "workspace-env-file"
	SecretsFile               = "secrets-file"
	FeatureSecretsFile        = "feature-secrets-file"
	InitEnv                   = "init-env"
	DisableDaemon             = "disable-daemon"
	CacheFrom                 = "cache-from"
	WorkspaceMountConsistency = "workspace-mount-consistency"
	Mount                     = "mount"
	Platform                  = "platform"
	PullFromInsideContainer   = "pull-from-inside-container"
	WorkspaceFolder           = "workspace-folder"
	IgnoreNotFound            = "ignore-not-found"
	GracePeriod               = "grace-period"
	Force                     = "force"
	RemoveVolumes             = "remove-volumes"
)

// IDE.
const (
	IDE       = "ide"
	IDEOption = "ide-option"
	IDELaunch = "ide-launch"
	Option    = "option"
)

// Podman.
const (
	Userns = "userns"
	UIDMap = "uidmap"
	GIDMap = "gidmap"
)

// Exec.
const (
	ContainerID = "container-id"
	DockerPath  = "docker-path"
	RemoteEnv   = "remote-env"
)

// Agent container setup (internal self-invocation).
const (
	ContainerWorkspaceInfo = "container-workspace-info"
	ChownWorkspace         = "chown-workspace"
	StreamMounts           = "stream-mounts"
	InjectGitCredentials   = "inject-git-credentials" //nolint:gosec // G101: flag name, not a credential
	AccessKey              = "access-key"
	WorkspaceHost          = "workspace-host"
	PlatformHost           = "platform-host"
)

// Dotfiles install (internal self-invocation).
const (
	Repository            = "repository"
	StrictHostKeyChecking = "strict-host-key-checking"
	InstallScript         = "install-script"
)

// Workspace list / status / describe / import.
const (
	SkipPro         = "skip-pro"
	ContainerStatus = "container-status"
	WorkspaceID     = "workspace-id"
	MachineID       = "machine-id"
	MachineReuse    = "machine-reuse"
	ProviderID      = "provider-id"
	ProviderReuse   = "provider-reuse"
	Data            = "data"
)

// Build command.
const (
	SkipDelete            = "skip-delete"
	Tag                   = "tag"
	SkipPush              = "skip-push"
	Push                  = "push"
	Label                 = "label"
	Output                = "output"
	ExperimentalLockfile  = "experimental-lockfile"
	ImageName             = "image-name"
	NoBuild               = "no-build"
	ForceBuild            = "force-build"
	ForceInternalBuildKit = "force-internal-buildkit"
)

// Testing-only (hidden).
const (
	DaemonInterval  = "daemon-interval"
	ForceDockerless = "force-dockerless"
)

// Common / global.
const (
	Debug           = "debug"
	Command         = "command"
	Context         = "context"
	User            = "user"
	Workdir         = "workdir"
	Home            = "home"
	LogOutput       = "log-output"
	LogFormat       = "log-format"
	ResultFormat    = "result-format"
	SetupInfo       = "setup-info"
	SecretsEnv      = "secrets-env"
	OwnerTrust      = "ownertrust"
	SocketPath      = "socketpath"
	GitKey          = "gitkey"
	ShutdownAction  = "shutdown-action"
	Timeout         = "timeout"
	Flavor          = "flavor"
	Verbose         = "verbose"
	Quiet           = "quiet"
	Owner           = "owner"
	AgentDir        = "agent-dir"
	UID             = "uid"
	OpenBrowser     = "open-browser"
	InheritListener = "inherit-listener"
	Config          = "config"
	JSON            = "json"
	Name            = "name"
	DryRun          = "dry-run"
)

// Feature / template commands.
const (
	BaseImage                    = "base-image"
	ForceCleanOutputFolder       = "force-clean-output-folder"
	GitHubOwner                  = "github-owner"
	GitHubRepo                   = "github-repo"
	IncludeFeaturesConfiguration = "include-features-configuration"
	IncludeMergedConfiguration   = "include-merged-configuration"
	OmitPaths                    = "omit-paths"
	OutputFolder                 = "output-folder"
	OverrideConfig               = "override-config"
	PreserveTestContainers       = "preserve-test-containers"
	ProjectFolder                = "project-folder"
	Registry                     = "registry"
	ShowDependencies             = "show-dependencies"
	ShowTags                     = "show-tags"
	SkipScenarios                = "skip-scenarios"
	Target                       = "target"
	TemplateArgs                 = "template-args"
	TemplateID                   = "template-id"
)

// Provider command.
const (
	Available      = "available"
	Create         = "create"
	FromExisting   = "from-existing"
	Prerelease     = "prerelease"
	ProviderSource = "provider-source"
	Use            = "use"
	Version        = "version"
)

// Pro / platform commands.
const (
	Channel          = "channel"
	ChartPath        = "chart-path"
	ChartRepo        = "chart-repo"
	DisplayName      = "display-name"
	Email            = "email"
	Header           = "header"
	HelmChartPath    = "helm-chart-path"
	HelmChartVersion = "helm-chart-version"
	HelmSet          = "helm-set"
	HelmValues       = "helm-values"
	Host             = "host"
	Insecure         = "insecure"
	Instance         = "instance"
	KubeContext      = "kube-context"
	Login            = "login"
	NoLogin          = "no-login"
	NoTunnel         = "no-tunnel"
	NoWait           = "no-wait"
	Own              = "own"
	Password         = "password"
	PreventWakeup    = "prevent-wakeup"
	Project          = "project"
	Provider         = "provider"
	ReuseValues      = "reuse-values"
	ServiceAccount   = "service-account"
	Token            = "token"
	Upgrade          = "upgrade"
	Values           = "values"
	Wait             = "wait"
)

// Internal / agent commands (self-invocation, daemons, tunnels, servers).
const (
	Address               = "address"
	AuthSockID            = "auth-sock-id"
	ConfigureDockerHelper = "configure-docker-helper"
	ConfigureGitHelper    = "configure-git-helper"
	Container             = "container"
	Daemon                = "daemon"
	Docker                = "docker"
	DockerArg             = "docker-arg"
	DockerCommand         = "docker-command"
	DockerImage           = "docker-image"
	Dry                   = "dry"
	ExecOutputCap         = "exec-output-cap"
	ExecTimeoutDefault    = "exec-timeout-default"
	ExecTimeoutMax        = "exec-timeout-max"
	ExtraPorts            = "extra-ports"
	Fail                  = "fail"
	FailOnErrorCode       = "fail-on-error-code"
	File                  = "file"
	FilterByOwner         = "filter-by-owner"
	FleetWorkspaceID      = "workspaceid"
	ForceBrowser          = "force-browser"
	GitUserSigningKey     = "git-user-signing-key"
	HelperImage           = "helper-image"
	Hidden                = "hidden"
	Interval              = "interval"
	KeyFile               = "key-file"
	MaxDepth              = "max-depth"
	Namespace             = "namespace"
	Port                  = "port"
	Request               = "request"
	SingleMachine         = "single-machine"
	SkipInit              = "skip-init"
	SkipOnCreate          = "skip-on-create"
	SkipUpdateContent     = "skip-update-content"
	TargetURL             = "target-url"
	Workspace             = "workspace"
	WorkspaceInfo         = "workspace-info"
	WorkspaceProject      = "workspace-project"
	WorkspaceUID          = "workspace-uid"
)
