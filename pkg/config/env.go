package config

// BoolTrue and BoolFalse are the string representations used for boolean
// configuration values throughout the application (env vars, options, agent config).
const (
	BoolTrue  = "true"
	BoolFalse = "false"
)

// Environment variable constants used throughout the application.
// All constants follow the EnvXxx naming convention.
const (
	// EnvBinaryPath is set to the path of the Devsy binary.
	EnvBinaryPath = "DEVSY"

	// EnvHome overrides the default Devsy home directory.
	EnvHome = "DEVSY_HOME"

	// EnvConfig overrides the default config file path.
	EnvConfig = "DEVSY_CONFIG"

	// EnvUI indicates the desktop UI is active.
	EnvUI = "DEVSY_UI"

	// EnvDebug enables debug logging.
	EnvDebug = "DEVSY_DEBUG"

	// EnvDisableTelemetry disables telemetry collection.
	EnvDisableTelemetry = "DEVSY_DISABLE_TELEMETRY"

	// EnvAgentURL overrides the agent download URL.
	EnvAgentURL = "DEVSY_AGENT_URL"

	// EnvAgentPreferDownload forces agent binary download even if a local copy exists.
	EnvAgentPreferDownload = "DEVSY_AGENT_PREFER_DOWNLOAD"

	// EnvOS is set to the host operating system (runtime.GOOS).
	EnvOS = "DEVSY_OS"

	// EnvArch is set to the host architecture (runtime.GOARCH).
	EnvArch = "DEVSY_ARCH"

	// EnvLogLevel is set to the current log level.
	EnvLogLevel = "DEVSY_LOG_LEVEL"

	// EnvWorkspaceID is the current workspace identifier.
	EnvWorkspaceID = "DEVSY_WORKSPACE_ID"

	// EnvWorkspaceUID is the current workspace unique identifier.
	EnvWorkspaceUID = "DEVSY_WORKSPACE_UID"

	// EnvWorkspaceDaemonConfig holds the workspace daemon configuration.
	EnvWorkspaceDaemonConfig = "DEVSY_WORKSPACE_DAEMON_CONFIG"

	// EnvWorkspaceCredentialsPort is the workspace credentials server port.
	EnvWorkspaceCredentialsPort = "DEVSY_WORKSPACE_CREDENTIALS_PORT" // #nosec G101

	// EnvCredentialsServerPort is the credentials server port on the host side.
	EnvCredentialsServerPort = "DEVSY_CREDENTIALS_SERVER_PORT" // #nosec G101

	// EnvGitHelperPort is the git credential helper forwarding port.
	EnvGitHelperPort = "DEVSY_GIT_HELPER_PORT"

	// EnvCraneName overrides the crane binary name.
	EnvCraneName = "DEVSY_CRANE_NAME"

	// EnvPlatformOptions holds serialized platform options.
	EnvPlatformOptions = "DEVSY_PLATFORM_OPTIONS"

	// EnvFlagsUp holds extra flags for the up command.
	EnvFlagsUp = "DEVSY_FLAGS_UP"

	// EnvFlagsSSH holds extra flags for the ssh command.
	EnvFlagsSSH = "DEVSY_FLAGS_SSH"

	// EnvFlagsDelete holds extra flags for the delete command.
	EnvFlagsDelete = "DEVSY_FLAGS_DELETE"

	// EnvFlagsStatus holds extra flags for the status command.
	EnvFlagsStatus = "DEVSY_FLAGS_STATUS"

	// EnvSubdomain is the subdomain configuration for Devsy Pro.
	EnvSubdomain = "DEVSY_SUBDOMAIN"

	// EnvPrefix is the base prefix for all Devsy environment variables.
	EnvPrefix = "DEVSY_"

	// EnvIDEPrefix is the prefix for IDE-specific option env vars (append IDE name + "_").
	EnvIDEPrefix = EnvPrefix + "IDE_"

	// EnvProviderPrefix is the prefix for provider-specific option env vars (append provider name + "_").
	EnvProviderPrefix = EnvPrefix + "PROVIDER_"

	// --- Provider-scoped env vars (set when running provider commands) ---.

	// EnvProviderWorkspaceID is the workspace identifier passed to providers.
	EnvProviderWorkspaceID = "WORKSPACE_ID"

	// EnvProviderWorkspaceUID is the workspace UID passed to providers.
	EnvProviderWorkspaceUID = "WORKSPACE_UID"

	// EnvProviderWorkspacePicture is the workspace picture URL passed to providers.
	EnvProviderWorkspacePicture = "WORKSPACE_PICTURE"

	// EnvProviderWorkspaceFolder is the workspace folder path passed to providers.
	EnvProviderWorkspaceFolder = "WORKSPACE_FOLDER"

	// EnvProviderWorkspaceContext is the workspace context passed to providers.
	EnvProviderWorkspaceContext = "WORKSPACE_CONTEXT"

	// EnvProviderWorkspaceOrigin is the workspace origin passed to providers.
	EnvProviderWorkspaceOrigin = "WORKSPACE_ORIGIN"

	// EnvProviderWorkspaceSource is the workspace source passed to providers.
	EnvProviderWorkspaceSource = "WORKSPACE_SOURCE"

	// EnvProviderWorkspaceProvider is the workspace provider name passed to providers.
	EnvProviderWorkspaceProvider = "WORKSPACE_PROVIDER"

	// EnvProviderMachineID is the machine identifier passed to providers.
	EnvProviderMachineID = "MACHINE_ID"

	// EnvProviderMachineContext is the machine context passed to providers.
	EnvProviderMachineContext = "MACHINE_CONTEXT"

	// EnvProviderMachineFolder is the machine folder path passed to providers.
	EnvProviderMachineFolder = "MACHINE_FOLDER"

	// EnvProviderMachineProvider is the machine provider name passed to providers.
	EnvProviderMachineProvider = "MACHINE_PROVIDER"

	// EnvProviderID is the provider identifier passed to providers.
	EnvProviderID = "PROVIDER_ID"

	// EnvProviderContext is the provider context passed to providers.
	EnvProviderContext = "PROVIDER_CONTEXT"

	// EnvProviderFolder is the provider folder path passed to providers.
	EnvProviderFolder = "PROVIDER_FOLDER"

	// EnvLoftProject is the Devsy project name for pro features.
	EnvLoftProject = "DEVSY_PROJECT"

	// EnvLoftFilterByOwner enables filtering by owner in Devsy.
	EnvLoftFilterByOwner = "DEVSY_FILTER_BY_OWNER"

	// EnvDevcontainerID is the devcontainer identifier.
	EnvDevcontainerID = "DEVCONTAINER_ID"
)
