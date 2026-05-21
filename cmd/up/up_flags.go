package up

import "github.com/spf13/cobra"

func (cmd *UpCmd) registerFlags(upCmd *cobra.Command) {
	cmd.registerSSHFlags(upCmd)
	cmd.registerDotfilesFlags(upCmd)
	cmd.registerDevContainerFlags(upCmd)
	cmd.registerIDEFlags(upCmd)
	cmd.registerGitFlags(upCmd)
	cmd.registerPodmanFlags(upCmd)
	cmd.registerWorkspaceFlags(upCmd)
	cmd.registerTestingFlags(upCmd)
}

func (cmd *UpCmd) registerSSHFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		BoolVar(&cmd.ConfigureSSH, "configure-ssh", true,
			"If true will configure the ssh config to include the Devsy workspace")
	upCmd.Flags().
		BoolVar(&cmd.GPGAgentForwarding, "gpg-agent-forwarding", false,
			"If true forward the local gpg-agent to the Devsy workspace")
	upCmd.Flags().
		StringVar(&cmd.SSHConfigPath, "ssh-config", "",
			"The path to the ssh config to modify, if empty will use ~/.ssh/config")
	upCmd.Flags().
		BoolVar(&cmd.SSHTunnelMode, "ssh-tunnel-mode", false,
			"If true will use a local TCP tunnel instead of ProxyCommand for SSH connections")
}

func (cmd *UpCmd) registerDotfilesFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.DotfilesSource, "dotfiles", "", "The path or url to the dotfiles to use in the container")
	upCmd.Flags().StringVar(&cmd.DotfilesSource, "dotfiles-repository", "", "Alias for --dotfiles")
	_ = upCmd.Flags().MarkHidden("dotfiles-repository")
	upCmd.Flags().
		StringVar(&cmd.DotfilesScript, "dotfiles-script", "",
			"The path in dotfiles directory to use to install the dotfiles, if empty will try to guess")
	upCmd.Flags().
		StringVar(&cmd.DotfilesTargetPath, "dotfiles-target-path", "",
			"The target path inside the container to install dotfiles to (e.g., ~/dotfiles)")
	upCmd.Flags().
		StringSliceVar(&cmd.DotfilesScriptEnv, "dotfiles-script-env", []string{},
			"Extra environment variables to put into the dotfiles install script, e.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().
		StringSliceVar(&cmd.DotfilesScriptEnvFile, "dotfiles-script-env-file", []string{},
			"The path to files containing environment variables to set for the dotfiles install script")
}

func (cmd *UpCmd) registerDevContainerFlags(upCmd *cobra.Command) {
	cmd.registerBuildFlags(upCmd)
	cmd.registerLifecycleFlags(upCmd)
	cmd.registerContainerOverrideFlags(upCmd)
}

func (cmd *UpCmd) registerBuildFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.DevContainerImage, "devcontainer-image", "",
			"The container image to use, this will override the devcontainer.json value in the project")
	upCmd.Flags().
		StringVar(&cmd.DevContainerPath, "devcontainer-path", "", "The path to the devcontainer.json relative to the project")
	upCmd.Flags().StringVar(&cmd.DevContainerPath, "config", "", "Alias for --devcontainer-path")
	_ = upCmd.Flags().MarkHidden("config")
	upCmd.Flags().
		StringVar(&cmd.DevContainerID, "devcontainer-id", "",
			"The ID of the devcontainer to use when multiple exist "+
				"(e.g., folder name in .devcontainer/FOLDER/devcontainer.json)")
	upCmd.Flags().
		StringVar(&cmd.ExtraDevContainerPath, "extra-devcontainer-path", "",
			"The path to an additional devcontainer.json file to override original devcontainer.json")
	upCmd.Flags().
		StringVar(&cmd.ExtraDevContainerPath, "override-config", "", "Alias for --extra-devcontainer-path")
	_ = upCmd.Flags().MarkHidden("override-config")
	upCmd.Flags().
		StringVar(&cmd.FallbackImage, "fallback-image", "",
			"The fallback image to use if no devcontainer configuration has been detected")
	upCmd.Flags().
		StringVar(&cmd.AdditionalFeatures, "additional-features", "",
			`Additional features to apply to the dev container (JSON as per "features" section in devcontainer.json)`)
	upCmd.Flags().
		StringArrayVar(&cmd.IDLabels, "id-label", []string{},
			"Override the default container identification labels (format: key=value, can be specified multiple times)")
	upCmd.Flags().
		StringVar(&cmd.DefaultUserEnvProbe, "default-user-env-probe", "",
			"Override userEnvProbe from devcontainer.json (loginInteractiveShell, loginShell, interactiveShell, none)")
	upCmd.Flags().
		StringVar(&cmd.GPUAvailability, "gpu-availability", "",
			"Override GPU availability detection (detect, true, false)")
	upCmd.Flags().
		StringVar(&cmd.UpdateRemoteUserUIDDefault, "update-remote-user-uid-default", "",
			"Default for updateRemoteUserUID when not set in devcontainer.json (on, off)")
	upCmd.Flags().
		StringVar(&cmd.ContainerDataFolder, "container-data-folder", "",
			"Custom path for container-specific data")
	defaultMountGitRoot := true
	cmd.MountWorkspaceGitRoot = &defaultMountGitRoot
	upCmd.Flags().
		BoolVar(cmd.MountWorkspaceGitRoot, "mount-workspace-git-root", true,
			"Mount the workspace git root as the workspace folder")
}

func (cmd *UpCmd) registerLifecycleFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		IntVar(&cmd.TerminalColumns, "terminal-columns", 0,
			"Terminal column count for lifecycle scripts")
	upCmd.Flags().
		IntVar(&cmd.TerminalRows, "terminal-rows", 0,
			"Terminal row count for lifecycle scripts")
	upCmd.Flags().
		BoolVar(&cmd.SkipPostCreate, "skip-post-create", false,
			"Skip the postCreateCommand lifecycle hook")
	upCmd.Flags().
		BoolVar(&cmd.SkipNonBlockingCommands, "skip-non-blocking-commands", false,
			"Skip non-blocking lifecycle commands")
	upCmd.Flags().
		BoolVar(&cmd.SkipPostStart, "skip-post-start", false,
			"Skip running postStartCommand")
	upCmd.Flags().
		BoolVar(&cmd.SkipPostAttach, "skip-post-attach", false,
			"Skip running postAttachCommand")
	upCmd.Flags().
		BoolVar(&cmd.SkipHostRequirements, "skip-host-requirements", false,
			"Skip host requirements validation and allow container creation even if the host does not meet minimum requirements")
}

func (cmd *UpCmd) registerContainerOverrideFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.ContainerUser, "container-user", "",
			"Override the user in the container")
	upCmd.Flags().
		StringVar(&cmd.RemoteUser, "remote-user", "",
			"Override the remoteUser setting")
}

func (cmd *UpCmd) registerIDEFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.IDE, "ide", "", "The IDE to open the workspace in. If empty will use vscode locally or in browser")
	upCmd.Flags().
		StringArrayVar(&cmd.IDEOptions, "ide-option", []string{}, "IDE option in the form KEY=VALUE")
	upCmd.Flags().
		BoolVar(&cmd.OpenIDE, "open-ide", true,
			"If this is false and an IDE is configured, Devsy will only install the IDE server backend, but not open it")
	upCmd.Flags().
		StringVar(&cmd.WorkspaceFolder, "workspace-folder", "",
			"Override the folder path opened in the IDE (absolute path inside the container)")
}

func (cmd *UpCmd) registerGitFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		Var(&cmd.GitCloneStrategy, "git-clone-strategy",
			"The git clone strategy Devsy uses to checkout git based workspaces. "+
				"Can be full (default), blobless, treeless or shallow")
	upCmd.Flags().
		BoolVar(&cmd.GitCloneRecursiveSubmodules, "git-clone-recursive-submodules", false,
			"If true will clone git submodule repositories recursively")
	upCmd.Flags().
		StringVar(&cmd.GitSSHSigningKey, "git-ssh-signing-key", "",
			"The ssh key to use when signing git commits. Used to explicitly setup Devsy's ssh signature "+
				"forwarding with given key. Should be same format as value of `git config user.signingkey`")
}

func (cmd *UpCmd) registerPodmanFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.Userns, "userns", "",
			"User namespace to use for the container (Podman only; e.g. \"keep-id\", \"host\", or \"auto\")")
	upCmd.Flags().
		StringSliceVar(&cmd.UidMap, "uidmap", []string{},
			"UID mapping for Podman user namespace "+
				"(Podman only; format: container_id:host_id:amount, e.g. \"0:1000:1\")")
	upCmd.Flags().
		StringSliceVar(&cmd.GidMap, "gidmap", []string{},
			"GID mapping for Podman user namespace "+
				"(Podman only; format: container_id:host_id:amount, e.g. \"0:1000:1\")")
}

func (cmd *UpCmd) registerWorkspaceFlags(upCmd *cobra.Command) {
	upCmd.Flags().StringVar(&cmd.ID, "id", "", "The id to use for the workspace")
	upCmd.Flags().
		StringVar(&cmd.Machine, "machine", "",
			"The machine to use for this workspace. The machine needs to exist beforehand or the "+
				"command will fail. If the workspace already exists, this option has no effect")
	upCmd.Flags().
		StringVar(&cmd.Source, "source", "", "Optional source for the workspace, e.g. git:https://github.com/my-org/my-repo")
	upCmd.Flags().
		StringArrayVar(&cmd.ProviderOptions, "provider-option", []string{}, "Provider option in the form KEY=VALUE")
	upCmd.Flags().
		BoolVar(&cmd.Reconfigure, "reconfigure", false,
			"Reconfigure the options for this workspace. Only supported in Devsy Pro right now.")
	upCmd.Flags().
		BoolVar(&cmd.Prebuild, "prebuild", false,
			"If true will only run the prebuild lifecycle (onCreateCommand + updateContentCommand) then stop")
	upCmd.Flags().
		BoolVar(&cmd.Recreate, "recreate", false, "If true will remove any existing containers and recreate them")
	upCmd.Flags().BoolVar(&cmd.Recreate, "remove-existing-container", false, "Alias for --recreate")
	_ = upCmd.Flags().MarkHidden("remove-existing-container")
	upCmd.Flags().
		BoolVar(&cmd.Reset, "reset", false,
			"If true will remove any existing containers including sources, and recreate them")
	upCmd.Flags().
		StringSliceVar(&cmd.PrebuildRepositories, "prebuild-repository", []string{},
			"Docker repository that hosts devsy prebuilds for this workspace")
	upCmd.Flags().
		StringArrayVar(&cmd.WorkspaceEnv, "workspace-env", []string{},
			"Extra env variables to put into the workspace, e.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().
		StringSliceVar(&cmd.WorkspaceEnvFile, "workspace-env-file", []string{},
			"The path to files containing a list of extra env variables to put into the workspace, "+
				"e.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().
		StringVar(&cmd.SecretsFile, "secrets-file", "",
			"Path to a dotenv-style file containing KEY=VALUE secrets injected into lifecycle commands")
	upCmd.Flags().
		StringVar(&cmd.FeatureSecretsFile, "feature-secrets-file", "",
			"Path to a JSON file containing secret values for features, format: "+
				`{"featureId": {"optionName": "value"}}`)
	upCmd.Flags().
		StringArrayVar(&cmd.InitEnv, "init-env", []string{},
			"Extra env variables to inject during the initialization of the workspace, e.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().
		BoolVar(&cmd.DisableDaemon, "disable-daemon", false,
			"If enabled, will not install a daemon into the target machine to track activity")
	upCmd.Flags().
		StringArrayVar(&cmd.CacheFrom, "cache-from", []string{},
			"Cache sources for the build (e.g., myregistry.io/cache:latest or type=registry,ref=...). "+
				"Takes priority over devcontainer.json build.cacheFrom")
	upCmd.Flags().
		StringVar(&cmd.WorkspaceMountConsistency, "workspace-mount-consistency", "",
			"Consistency mode for the workspace bind mount (consistent, cached, delegated)")
	upCmd.Flags().
		StringArrayVar(&cmd.Mounts, "mount", []string{},
			"Additional mount to add to the container (format: type=bind,source=/host/path,target=/container/path). "+
				"Can be specified multiple times")
}

func (cmd *UpCmd) registerTestingFlags(upCmd *cobra.Command) {
	upCmd.Flags().StringVar(&cmd.DaemonInterval, "daemon-interval", "", "TESTING ONLY")
	_ = upCmd.Flags().MarkHidden("daemon-interval")
	upCmd.Flags().BoolVar(&cmd.ForceDockerless, "force-dockerless", false, "TESTING ONLY")
	_ = upCmd.Flags().MarkHidden("force-dockerless")
}
