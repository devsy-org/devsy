package up

import (
	"github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/spf13/cobra"
)

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
	flags.Add(upCmd,
		flags.Bool(&cmd.ConfigureSSH, names.SSHConfigure, true,
			"Add the workspace to your SSH config"),
		flags.Bool(&cmd.GPGAgentForwarding, names.SSHGPGForwarding, false,
			"Forward the local gpg-agent into the workspace"),
		flags.String(&cmd.SSHConfigPath, names.SSHConfig, "",
			"Path to the SSH config to modify (default ~/.ssh/config)"),
		flags.Bool(&cmd.SSHTunnelMode, names.SSHTunnel, false,
			"Use a local TCP tunnel instead of ProxyCommand for SSH connections"),
	)
}

func (cmd *UpCmd) registerDotfilesFlags(upCmd *cobra.Command) {
	flags.Add(upCmd,
		flags.String(&cmd.DotfilesSource, names.Dotfiles, "",
			"Path or URL to dotfiles to install in the container"),
		flags.String(&cmd.DotfilesScript, names.DotfilesScript, "",
			"Script within the dotfiles to run (auto-detected if unset)"),
		flags.String(&cmd.DotfilesTargetPath, names.DotfilesTargetPath, "",
			"Path inside the container to install dotfiles to (e.g. ~/dotfiles)"),
		flags.StringSlice(&cmd.DotfilesScriptEnv, names.DotfilesScriptEnv, nil,
			"Env var for the dotfiles script (KEY=VALUE, repeatable)"),
		flags.StringSlice(&cmd.DotfilesScriptEnvFile, names.DotfilesScriptEnvFile, nil,
			"File of env vars for the dotfiles script"),
	)
}

func (cmd *UpCmd) registerDevContainerFlags(upCmd *cobra.Command) {
	cmd.registerBuildFlags(upCmd)
	cmd.registerLifecycleFlags(upCmd)
	cmd.registerContainerOverrideFlags(upCmd)
}

func (cmd *UpCmd) registerBuildFlags(upCmd *cobra.Command) {
	defaultMountGitRoot := true
	cmd.MountWorkspaceGitRoot = &defaultMountGitRoot
	flags.Add(upCmd,
		flags.String(&cmd.DevContainerSource, names.DevContainer, "",
			"Select the devcontainer config source, overriding project discovery: "+
				`"none" (ignore the project config), "image:<ref>" (use only that image), `+
				`"id:<name>" (a named .devcontainer/<name> profile), or a path to a devcontainer.json`),
		flags.String(&cmd.ExtraDevContainerPath, names.DevContainerOverlay, "",
			"Path to a devcontainer.json whose values merge on top of the resolved config"),
		flags.String(&cmd.FallbackImage, names.FallbackImage, "",
			"Image to use when no devcontainer config is found"),
		flags.String(&cmd.GPUAvailability, names.GPUAvailability, "",
			"Override GPU detection (detect, true, false)"),
		flags.String(&cmd.UpdateRemoteUserUIDDefault, names.UpdateRemoteUserUID, "",
			"Default for updateRemoteUserUID when unset in the config (on, off)"),
		flags.Bool(cmd.MountWorkspaceGitRoot, names.MountWorkspaceGitRoot, true,
			"Mount the workspace git root as the workspace folder"),
		flags.Bool(&cmd.FrozenLockfile, names.FrozenLockfile, false,
			"Fail if devcontainer-lock.json is missing or does not match the resolved features "+
				"instead of writing it"),
		flags.Bool(&cmd.NoLockfile, names.NoLockfile, false,
			"Disable devcontainer-lock.json generation and verification"),
	)
	flags.RegisterDevContainerModifierFlags(upCmd.Flags(), flags.DevContainerModifierFlags{
		Image:               &cmd.DevContainerImage,
		Features:            &cmd.AdditionalFeatures,
		UserEnvProbe:        &cmd.DefaultUserEnvProbe,
		IDLabels:            &cmd.IDLabels,
		ContainerDataFolder: &cmd.ContainerDataFolder,
	})
}

func (cmd *UpCmd) registerLifecycleFlags(upCmd *cobra.Command) {
	flags.Add(upCmd,
		flags.Int(&cmd.TerminalColumns, names.TerminalColumns, 0,
			"Terminal column count for lifecycle scripts"),
		flags.Int(&cmd.TerminalRows, names.TerminalRows, 0,
			"Terminal row count for lifecycle scripts"),
		flags.Bool(&cmd.SkipPostCreate, names.SkipPostCreate, false,
			"Skip the postCreateCommand hook"),
		flags.Bool(&cmd.SkipNonBlockingCommands, names.SkipNonBlockingCommands, false,
			"Skip non-blocking lifecycle commands"),
		flags.Bool(&cmd.SkipPostStart, names.SkipPostStart, false,
			"Skip the postStartCommand hook"),
		flags.Bool(&cmd.SkipPostAttach, names.SkipPostAttach, false,
			"Skip the postAttachCommand hook"),
		flags.Bool(&cmd.SkipHostRequirements, names.SkipHostRequirements, false,
			"Skip host requirements validation"),
	)
}

func (cmd *UpCmd) registerContainerOverrideFlags(upCmd *cobra.Command) {
	flags.Add(upCmd,
		flags.String(&cmd.ContainerUser, names.ContainerUser, "",
			"Override the container user"),
		flags.String(&cmd.RemoteUser, names.RemoteUser, "",
			"Override the remoteUser setting"),
	)
}

func (cmd *UpCmd) registerIDEFlags(upCmd *cobra.Command) {
	cmd.IDELaunch = opener.LaunchAuto
	flags.Add(
		upCmd,
		flags.String(&cmd.IDE, names.IDE, "",
			"IDE to open the workspace in (defaults to vscode, locally or in browser)"),
		flags.StringArray(
			&cmd.IDEOptions,
			names.IDEOption,
			nil,
			"IDE option (KEY=VALUE, repeatable)",
		),
		flags.Value(
			&cmd.IDELaunch,
			names.IDELaunch,
			"How to launch the IDE: auto opens it (default), headless skips the host browser/app, skip does not launch",
		),
		flags.String(&cmd.WorkspaceFolder, names.WorkspaceFolder, "",
			"Folder to open in the IDE (absolute path inside the container)"),
	)
}

func (cmd *UpCmd) registerGitFlags(upCmd *cobra.Command) {
	flags.Add(upCmd,
		flags.Value(&cmd.GitCloneStrategy, names.GitCloneStrategy,
			"Clone strategy for git workspaces: full (default), blobless, treeless or shallow"),
		flags.Bool(&cmd.GitCloneRecursiveSubmodules, names.GitRecurseSubmodules, false,
			"Clone submodules recursively"),
		flags.Value(&cmd.GitLFSMode, names.GitLFSMode,
			"Git LFS handling after cloning: full (default, download content), "+
				"setup-only (configure LFS, leave pointer files) or skip (ignore LFS)"),
		flags.String(&cmd.GitSSHSigningKey, names.GitSSHSigningKey, "",
			"SSH key to sign git commits with (same format as git config user.signingkey)"),
	)
}

func (cmd *UpCmd) registerPodmanFlags(upCmd *cobra.Command) {
	flags.Add(upCmd,
		flags.String(
			&cmd.Userns,
			names.Userns,
			"",
			"User namespace to use for the container (Podman only; e.g. \"keep-id\", \"host\", or \"auto\")",
		),
		flags.StringSlice(&cmd.UidMap, names.UIDMap, nil,
			"UID mapping for Podman user namespace "+
				"(Podman only; format: container_id:host_id:amount, e.g. \"0:1000:1\")"),
		flags.StringSlice(&cmd.GidMap, names.GIDMap, nil,
			"GID mapping for Podman user namespace "+
				"(Podman only; format: container_id:host_id:amount, e.g. \"0:1000:1\")"),
	)
	upCmd.MarkFlagsMutuallyExclusive(names.Userns, names.UIDMap)
	upCmd.MarkFlagsMutuallyExclusive(names.Userns, names.GIDMap)
}

func (cmd *UpCmd) registerWorkspaceFlags(upCmd *cobra.Command) {
	flags.Add(
		upCmd,
		flags.String(&cmd.ID, names.ID, "", "ID for the workspace"),
		flags.String(
			&cmd.Machine,
			names.Machine,
			"",
			"Existing machine to use for this workspace; no effect if the workspace already exists",
		),
		flags.String(&cmd.Source, names.Source, "",
			"Workspace source (e.g. git:https://github.com/my-org/my-repo)"),
		flags.StringArray(&cmd.ProviderOptions, names.ProviderOption, nil,
			"Provider option (KEY=VALUE, repeatable)"),
		flags.Bool(&cmd.Reconfigure, names.Reconfigure, false,
			"Reconfigure this workspace's options (Devsy Pro only)"),
		flags.Bool(&cmd.Prebuild, names.Prebuild, false,
			"Run only the prebuild lifecycle (onCreateCommand + updateContentCommand), then stop"),
		flags.Bool(&cmd.Pull, names.Pull, false, "Pull a newer base image when building"),
		flags.Bool(&cmd.NoCache, names.NoCache, false, "Build without the image cache"),
		flags.Bool(
			&cmd.Recreate,
			names.Recreate,
			false,
			"Remove and recreate existing containers",
		),
		flags.Bool(&cmd.Reset, names.Reset, false,
			"Remove and recreate existing containers, including their sources"),
		flags.StringSlice(&cmd.PrebuildRepositories, names.PrebuildRepo, nil,
			"Docker repository hosting prebuilds for this workspace"),
		flags.StringArray(&cmd.WorkspaceEnv, names.WorkspaceEnv, nil,
			"Env var for the workspace (KEY=VALUE, repeatable)"),
		flags.StringSlice(&cmd.WorkspaceEnvFile, names.WorkspaceEnvFile, nil,
			"File of env vars for the workspace"),
		flags.String(&cmd.SecretsFile, names.SecretsFile, "",
			`JSON file ({"KEY":"value"}) of secrets for lifecycle commands`),
		flags.StringArray(&cmd.Secrets, names.Secret, nil,
			"Stored Devsy secret to inject, as NAME[,type=env|mount][,target=X]; "+
				"type=env (default) sets an env var, type=mount writes /run/secrets/<target>. Repeatable"),
		flags.StringArray(&cmd.EnvVars, names.Env, nil,
			"Stored Devsy env var to inject into the workspace as NAME[=TARGET]. Repeatable"),
		flags.StringArray(&cmd.BuildSecretNames, names.BuildSecret, nil,
			"Stored Devsy secret exposed to the build via BuildKit "+
				"(RUN --mount=type=secret,id=NAME). Repeatable"),
		flags.String(&cmd.GitTokenSecret, names.GitToken, "",
			"Stored Devsy secret holding an access token for cloning a private HTTP repository"),
		flags.String(&cmd.GitTokenUsername, names.GitTokenUsername, "",
			"Username for --git-token (default inferred from the repo host)"),
		flags.String(&cmd.FeatureSecretsFile, names.FeatureSecretsFile, "",
			`JSON file of feature secret values, format: {"featureId": {"optionName": "value"}}`),
		flags.StringArray(&cmd.InitEnv, names.InitEnv, nil,
			"Env var for workspace initialization (KEY=VALUE, repeatable)"),
		flags.Bool(&cmd.DisableDaemon, names.DisableDaemon, false,
			"Do not install the activity-tracking daemon on the target machine"),
		flags.StringArray(&cmd.CacheFrom, names.CacheFrom, nil,
			"Build cache source (e.g. myregistry.io/cache:latest or type=registry,ref=...); "+
				"takes priority over devcontainer.json build.cacheFrom"),
		flags.String(&cmd.WorkspaceMountConsistency, names.WorkspaceMountConsistency, "",
			"Consistency mode for the workspace bind mount (consistent, cached, delegated)"),
		flags.StringArray(
			&cmd.Mounts,
			names.Mount,
			nil,
			"Extra container mount (type=bind,source=/host/path,target=/container/path; repeatable)",
		),
		flags.String(
			&cmd.RunPlatform,
			names.Platform,
			"",
			"Run under a specific platform via emulation (e.g. linux/amd64); empty uses the host platform",
		),
		flags.Bool(
			&cmd.pullFromInsideContainerFlag,
			names.PullFromInsideContainer,
			false,
			"Clone the source inside the container instead of bind-mounting from the host (unset = auto-detect)",
		),
	)
}

func (cmd *UpCmd) registerTestingFlags(upCmd *cobra.Command) {
	flags.Add(upCmd,
		flags.String(&cmd.DaemonInterval, names.DaemonInterval, "", "TESTING ONLY").Hidden(),
		flags.Bool(&cmd.ForceDockerless, names.ForceDockerless, false, "TESTING ONLY").Hidden(),
	)
}
