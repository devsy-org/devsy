package names

import "fmt"

func Flag(name string) string { return "--" + name }

func FlagValue(name, value string) string { return fmt.Sprintf("--%s=%s", name, value) }

func FlagTrue(name string) string { return FlagValue(name, "true") }

func FlagFalse(name string) string { return FlagValue(name, "false") }

// Workspace.
const (
	BuildSecret               = "build-secret"
	CacheFrom                 = "cache-from"
	ContainerDataFolder       = "container-data-folder"
	ContainerStatus           = "container-status"
	ContainerUser             = "container-user"
	Data                      = "data"
	DevContainer              = "devcontainer"
	DevContainerID            = "devcontainer-id"
	DevContainerImage         = "devcontainer-image"
	DevContainerOverlay       = "devcontainer-overlay"
	DevContainerPath          = "devcontainer-path"
	DisableDaemon             = "disable-daemon"
	Env                       = "env"
	FallbackImage             = "fallback-image"
	Features                  = "features"
	FeatureSecretsFile        = "feature-secrets-file"
	Force                     = "force"
	GPUAvailability           = "gpu-availability"
	GracePeriod               = "grace-period"
	ID                        = "id"
	IDLabel                   = "id-label"
	IgnoreNotFound            = "ignore-not-found"
	InitEnv                   = "init-env"
	Machine                   = "machine"
	MachineID                 = "machine-id"
	MachineReuse              = "machine-reuse"
	Mount                     = "mount"
	MountWorkspaceGitRoot     = "mount-workspace-git-root"
	NoAutoStart               = "no-auto-start"
	NoCache                   = "no-cache"
	Platform                  = "platform"
	Prebuild                  = "prebuild"
	PrebuildRepo              = "prebuild-repo"
	ProviderID                = "provider-id"
	ProviderOption            = "provider-option"
	ProviderReuse             = "provider-reuse"
	Pull                      = "pull"
	PullFromInsideContainer   = "pull-from-inside-container"
	Reconfigure               = "reconfigure"
	Recovery                  = "recovery"
	Recreate                  = "recreate"
	RemoteUser                = "remote-user"
	RemoveVolumes             = "remove-volumes"
	Reset                     = "reset"
	Secret                    = "secret"
	SecretsFile               = "secrets-file"
	SkipPro                   = "skip-pro"
	Source                    = "source"
	UpdateRemoteUserUID       = "update-remote-user-uid"
	UserEnvProbe              = "user-env-probe"
	WorkspaceEnv              = "workspace-env"
	WorkspaceEnvFile          = "workspace-env-file"
	WorkspaceFolder           = "workspace-folder"
	WorkspaceID               = "workspace-id"
	WorkspaceMountConsistency = "workspace-mount-consistency"
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
	GitToken             = "git-token"
	GitTokenUsername     = "git-token-username"
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

// Agent container.
const (
	ContainerWorkspaceInfo = "container-workspace-info"
	ChownWorkspace         = "chown-workspace"
	StreamMounts           = "stream-mounts"
	InjectGitCredentials   = "inject-git-credentials" //nolint:gosec // G101: flag name, not a credential
	AccessKey              = "access-key"
	WorkspaceHost          = "workspace-host"
	PlatformHost           = "platform-host"
)

// Dotfiles.
const (
	Repository            = "repository"
	StrictHostKeyChecking = "strict-host-key-checking"
	InstallScript         = "install-script"
)

// Build.
const (
	SkipDelete            = "skip-delete"
	Tag                   = "tag"
	SkipPush              = "skip-push"
	Push                  = "push"
	Label                 = "label"
	Output                = "output"
	ExperimentalLockfile  = "experimental-lockfile"
	FrozenLockfile        = "frozen-lockfile"
	NoLockfile            = "no-lockfile"
	ImageName             = "image-name"
	NoBuild               = "no-build"
	ForceBuild            = "force-build"
	ForceInternalBuildKit = "force-internal-buildkit"
)

// Testing.
const (
	DaemonInterval  = "daemon-interval"
	ForceDockerless = "force-dockerless"
)

// Secret / env values.
const (
	Value    = "value"
	FromFile = "from-file"
	Stdin    = "stdin"
)

// Miscellaneous.
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

// Internal / agent commands.
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
