package config

import "al.essio.dev/pkg/shellescape"

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
	DevContainerResultPath         = "/var/run/" + BinaryName + "/result.json"
	DevContainerResultSelectorPath = "/var/run/" + BinaryName + "/result.path"

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
	DevContainerResultFallbackPath         = ContainerDataDirFallback + "/result.json"
	DevContainerResultFallbackSelectorPath = ContainerDataDirFallback + "/result.path"

	// ContainerDevsyHelperLocation is where the Devsy agent binary lives inside containers.
	ContainerDevsyHelperLocation = "/usr/local/bin/" + BinaryName

	// RemoteDevsyHelperLocation is the staging path for the Devsy agent on remote hosts.
	RemoteDevsyHelperLocation = "/tmp/" + BinaryName

	// ContainerActivityFile is touched by SSH/fleet servers to record container liveness.
	ContainerActivityFile = "/tmp/" + BinaryName + ".activity"

	// WorkspaceBusyFile is the per-workspace lock file written under the workspace folder.
	WorkspaceBusyFile = "workspace.lock"
)

// ReadDevContainerResultCommand returns a command that reads the result selected
// by the setup process. It fails when no valid selector exists.
func ReadDevContainerResultCommand() string {
	return readDevContainerResultCommand(
		DevContainerResultPath,
		DevContainerResultFallbackPath,
		DevContainerResultSelectorPath,
		DevContainerResultFallbackSelectorPath,
	)
}

func readDevContainerResultCommand(
	primary, fallback, primarySelector, fallbackSelector string,
) string {
	primary = shellescape.Quote(primary)
	fallback = shellescape.Quote(fallback)
	primarySelector = shellescape.Quote(primarySelector)
	fallbackSelector = shellescape.Quote(fallbackSelector)

	return "if [ -f " + primarySelector + " ] && [ -f " + primary +
		" ] && [ \"$(cat " + primarySelector + ")\" = " + primary +
		" ]; then cat " + primary +
		"; elif [ -f " + fallbackSelector + " ] && [ -f " + fallback +
		" ] && [ \"$(cat " + fallbackSelector + ")\" = " + fallback +
		" ]; then cat " + fallback +
		"; else echo 'devsy result path selector is missing' >&2; exit 1; fi"
}
