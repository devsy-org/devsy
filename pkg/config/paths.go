package config

const (
	// IgnoreFileName is the name of the devsy ignore file.
	IgnoreFileName = "." + BinaryName + "ignore"

	// SSHSignatureHelperPath is the path to the SSH signature helper script.
	SSHSignatureHelperPath = "/usr/local/bin/" + BinaryName + "-ssh-signature"

	// SSHSignatureHelperName is the name used in git config for the SSH signature program.
	SSHSignatureHelperName = BinaryName + "-ssh-signature"

	// DockerCredentialHelperName is the docker credential helper binary name.
	DockerCredentialHelperName = "docker-credential-" + BinaryName

	// DevContainerResultPath is where devcontainer results are written.
	DevContainerResultPath = "/var/run/" + BinaryName + "/result.json"

	// DaemonProcessName is the name used for the fallback background daemon process
	// PID file and lock file in os.TempDir().
	DaemonProcessName = BinaryName + ".daemon"

	// ContainerDataDir is the base directory for Devsy data inside containers.
	ContainerDataDir = "/var/" + BinaryName

	// ContainerDevsyHelperLocation is where the Devsy agent binary lives inside containers.
	ContainerDevsyHelperLocation = "/usr/local/bin/" + BinaryName

	// RemoteDevsyHelperLocation is the staging path for the Devsy agent on remote hosts.
	RemoteDevsyHelperLocation = "/tmp/" + BinaryName

	// ContainerActivityFile is touched by SSH/fleet servers to record container liveness.
	ContainerActivityFile = "/tmp/" + BinaryName + ".activity"

	// WorkspaceBusyFile is the per-workspace lock file written under the workspace folder.
	WorkspaceBusyFile = "workspace.lock"
)
