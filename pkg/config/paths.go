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

	// ContainerDataDirFallback is used instead of ContainerDataDir when a
	// non-root container (e.g. an OpenShift restricted-SCC pod) cannot create
	// /var/devsy.
	ContainerDataDirFallback = "/tmp/" + BinaryName + "-data"

	// DevContainerResultFallbackPath mirrors DevContainerResultPath under
	// ContainerDataDirFallback.
	DevContainerResultFallbackPath = ContainerDataDirFallback + "/result.json"

	// ContainerDevsyHelperLocation is where the Devsy agent binary lives inside containers.
	ContainerDevsyHelperLocation = "/usr/local/bin/" + BinaryName

	// RemoteDevsyHelperLocation is the staging path for the Devsy agent on remote hosts.
	RemoteDevsyHelperLocation = "/tmp/" + BinaryName

	// ContainerActivityFile is touched by SSH/fleet servers to record container liveness.
	ContainerActivityFile = "/tmp/" + BinaryName + ".activity"

	// WorkspaceBusyFile is the per-workspace lock file written under the workspace folder.
	WorkspaceBusyFile = "workspace.lock"
)

// ReadDevContainerResultCommand returns the shell command that reads the
// devcontainer result file over exec, trying DevContainerResultPath first
// and falling back to DevContainerResultFallbackPath: a non-root container
// may have had to write to the fallback location, and this lets the host
// find it without needing separate coordination of which one was used.
func ReadDevContainerResultCommand() string {
	return "cat " + DevContainerResultPath + " 2>/dev/null || cat " + DevContainerResultFallbackPath
}
