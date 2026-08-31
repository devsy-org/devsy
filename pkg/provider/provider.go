package provider

import (
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/types"
	"sigs.k8s.io/yaml"
)

const (
	CommandEnv = "COMMAND"
)

type ProviderConfig struct {
	// Name is the name of the provider
	Name string `json:"name,omitempty"`

	// Version is the provider version
	Version string `json:"version,omitempty"`

	// Icon holds an image URL that will be displayed
	Icon string `json:"icon,omitempty"`

	// IconDark holds an image URL that will be displayed in dark mode
	IconDark string `json:"iconDark,omitempty"`

	// Home holds the provider home URL
	Home string `json:"home,omitempty"`

	// Source is the source the provider was loaded from
	Source ProviderSource `json:"source"`

	// Description is the provider description
	Description string `json:"description,omitempty"`

	// OptionGroups holds information how to display options
	OptionGroups []ProviderOptionGroup `json:"optionGroups,omitempty"`

	// Options are the provider options.
	Options map[string]*types.Option `json:"options,omitempty"`

	// Agent allows you to override agent configuration
	Agent ProviderAgentConfig `json:"agent"`

	// Exec holds the provider commands
	Exec ProviderCommands `json:"exec"`

	// Binaries is an optional field to specify a binary to execute the commands
	Binaries map[string][]*ProviderBinary `json:"binaries,omitempty"`
}

type ProviderOptionGroup struct {
	// Name is the display name of the option group
	Name string `json:"name,omitempty"`

	// Options are the options that belong to this group
	Options []string `json:"options,omitempty"`

	// DefaultVisible defines if the option group should be visible by default
	DefaultVisible bool `json:"defaultVisible,omitempty"`
}

type ProviderSource struct {
	// Internal means provider was received internally
	Internal bool `json:"internal,omitempty"`

	// Github source for the provider
	Github string `json:"github,omitempty"`

	// File source for the provider
	File string `json:"file,omitempty"`

	// URL where the provider was downloaded from
	URL string `json:"url,omitempty"`

	// Raw is the exact string we used to load the provider
	Raw string `json:"raw,omitempty"`
}

type ProviderAgentConfig struct {
	// Local defines if Devsy is running locally
	Local types.StrBool `json:"local,omitempty"`

	// Path is the binary path inside the server devsy will expect the agent binary
	Path string `json:"path,omitempty"`

	// DataPath is the agent path where data is stored
	DataPath string `json:"dataPath,omitempty"`

	// DownloadURL is the base url where to download the agent from
	DownloadURL string `json:"downloadURL,omitempty"`

	// Timeout is the timeout in minutes to wait until the agent tries
	// to turn of the server.
	Timeout string `json:"inactivityTimeout,omitempty"`

	// ContainerTimeout is the timeout in minutes to wait until the agent tries
	// to delete the container.
	ContainerTimeout string `json:"containerInactivityTimeout,omitempty"`

	// InjectGitCredentials signals Devsy if git credentials should get synced into
	// the remote machine for cloning the repository.
	InjectGitCredentials types.StrBool `json:"injectGitCredentials,omitempty"`

	// InjectDockerCredentials signals Devsy if docker credentials should get synced
	// into the remote machine for pulling and pushing images.
	InjectDockerCredentials types.StrBool `json:"injectDockerCredentials,omitempty"`

	// Exec commands that can be used on the remote
	Exec ProviderAgentConfigExec `json:"exec"`

	// Binaries is an optional field to specify a binary to execute the commands
	Binaries map[string][]*ProviderBinary `json:"binaries,omitempty"`

	// Dockerless holds custom dockerless configuration
	Dockerless ProviderDockerlessOptions `json:"dockerless"`

	// Driver is the driver to use for deploying the devcontainer. Currently supports
	// docker (default) or kubernetes (experimental)
	Driver string `json:"driver,omitempty"`

	// Docker holds docker specific configuration
	Docker ProviderDockerDriverConfig `json:"docker"`

	// Custom holds custom driver specific configuration
	Custom ProviderCustomDriverConfig `json:"custom"`

	// Kubernetes holds kubernetes specific configuration
	Kubernetes ProviderKubernetesDriverConfig `json:"kubernetes"`

	// Apple holds Apple container specific configuration
	Apple ProviderAppleDriverConfig `json:"apple"`

	// Microsandbox holds microsandbox microVM specific configuration
	Microsandbox ProviderMicrosandboxDriverConfig `json:"microsandbox"`
}

type ProviderDockerlessOptions struct {
	// Disabled signals if dockerless building is disabled
	Disabled types.StrBool `json:"disabled,omitempty"`

	// Image is the image of the dockerless container to start
	Image string `json:"image,omitempty"`

	// IgnorePaths are additional ignore paths that should be ignored during deletion
	IgnorePaths string `json:"ignorePaths,omitempty"`

	// Registry to use as remote cache
	RegistryCache string `json:"registryCache,omitempty"`

	// DisableDockerCredentials prevents docker credentials from getting injected
	DisableDockerCredentials types.StrBool `json:"disableDockerCredentials,omitempty"`
}

func (a ProviderAgentConfig) IsDockerDriver() bool {
	return a.Driver == "" || a.Driver == DockerDriver
}

// ContainerInstallPath is where the agent binary lives inside the container
// for this driver: config.ContainerDevsyHelperLocation by default, or the
// Kubernetes driver's AgentInstallPath override when set (a writable path
// for non-root containers, e.g. an OpenShift restricted-SCC pod).
func (a ProviderAgentConfig) ContainerInstallPath() string {
	if a.Driver == KubernetesDriver && a.Kubernetes.AgentInstallPath != "" {
		return a.Kubernetes.AgentInstallPath
	}
	return config.ContainerDevsyHelperLocation
}

// runAsFields is the subset of corev1.SecurityContext this package needs to
// judge effective non-root execution, without depending on k8s.io/api.
type runAsFields struct {
	RunAsUser    *int64 `json:"runAsUser,omitempty"`
	RunAsNonRoot *bool  `json:"runAsNonRoot,omitempty"`
}

// unmarshalInlineOrFile parses raw as inline YAML into out, falling back to
// treating raw as a file path on failure. It mirrors the Kubernetes driver's
// own dual-mode parsing of AGENT_SECURITY_CONTEXT and POD_MANIFEST_TEMPLATE
// (pkg/driver/kubernetes: parseSecurityContext, getPodTemplate).
func unmarshalInlineOrFile(raw string, out any) error {
	if err := yaml.Unmarshal([]byte(raw), out); err == nil {
		return nil
	}
	p, err := filepath.Abs(raw)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(
		p,
	) // #nosec G304 -- path comes from the operator-controlled provider config, not untrusted input
	if err != nil {
		return err
	}
	return yaml.Unmarshal(body, out)
}

// devsyContainerRunAsFields extracts the run-as-user fields of the "devsy"
// container's securityContext from a podManifestTemplate, or nil if the
// template is empty, unparsable, or sets no such container.
func devsyContainerRunAsFields(podManifestTemplate string) *runAsFields {
	if podManifestTemplate == "" {
		return nil
	}
	var pod minimalPodManifest
	if err := unmarshalInlineOrFile(podManifestTemplate, &pod); err != nil {
		return nil
	}
	for _, c := range pod.Spec.Containers {
		if c.Name == config.BinaryName {
			return c.SecurityContext
		}
	}
	return nil
}

// minimalPodManifest is the subset of corev1.Pod this package needs to
// resolve a podManifestTemplate's per-container run-as-user override,
// without depending on k8s.io/api.
type minimalPodManifest struct {
	Spec minimalPodSpec `json:"spec"`
}

type minimalPodSpec struct {
	Containers []minimalContainer `json:"containers"`
}

type minimalContainer struct {
	Name            string       `json:"name"`
	SecurityContext *runAsFields `json:"securityContext,omitempty"`
}

// effectiveKubernetesRunAsFields resolves the run-as-user fields Devsy's
// Kubernetes driver actually applies to the "devsy" container: a named
// "devsy" container securityContext in podManifestTemplate has the highest
// precedence and overrides agentSecurityContext field by field (pkg/driver/
// kubernetes: resolveContainerSecurityContext, mergeContainer).
func effectiveKubernetesRunAsFields(k ProviderKubernetesDriverConfig) *runAsFields {
	var sc runAsFields
	haveAny := false
	if k.AgentSecurityContext != "" {
		if err := unmarshalInlineOrFile(k.AgentSecurityContext, &sc); err == nil {
			haveAny = true
		}
	}
	if override := devsyContainerRunAsFields(k.PodManifestTemplate); override != nil {
		if override.RunAsUser != nil {
			sc.RunAsUser = override.RunAsUser
			haveAny = true
		}
		if override.RunAsNonRoot != nil {
			sc.RunAsNonRoot = override.RunAsNonRoot
			haveAny = true
		}
	}
	if !haveAny {
		return nil
	}
	return &sc
}

// RunsFixedNonRootUser reports whether the effective Kubernetes container
// security context (AGENT_SECURITY_CONTEXT, as overridden field by field by
// any named "devsy" container in POD_MANIFEST_TEMPLATE) explicitly
// guarantees the container runs as a fixed non-root UID (an OpenShift
// restricted SCC, for example), so there is no root to su from: an su into
// the remote user would only ever fail, not drop privilege, and must be
// skipped. STRICT_SECURITY alone only clears the hardcoded root fields; it
// does not guarantee which UID the cluster ends up assigning, so it is not
// treated as a signal here.
func (a ProviderAgentConfig) RunsFixedNonRootUser() bool {
	if a.Driver != KubernetesDriver {
		return false
	}
	sc := effectiveKubernetesRunAsFields(a.Kubernetes)
	if sc == nil {
		return false
	}
	return (sc.RunAsNonRoot != nil && *sc.RunAsNonRoot) ||
		(sc.RunAsUser != nil && *sc.RunAsUser != 0)
}

const (
	DockerDriver       = "docker"
	KubernetesDriver   = "kubernetes"
	CustomDriver       = "custom"
	AppleDriver        = "apple"
	MicrosandboxDriver = "microsandbox"
)

// ProviderAppleDriverConfig holds configuration for the Apple container driver,
// which runs Linux containers as lightweight VMs on Apple silicon (macOS 26+).
type ProviderAppleDriverConfig struct {
	// Path where to find the `container` binary, defaults to 'container'
	Path string `json:"path,omitempty"`

	// Rosetta enables x86_64 emulation inside the guest.
	Rosetta types.StrBool `json:"rosetta,omitempty"`

	// Environment variables to set when running `container` commands
	Env map[string]string `json:"env,omitempty"`
}

// ProviderMicrosandboxDriverConfig holds configuration for the microsandbox
// driver, which boots the devcontainer OCI image as a hardware-isolated microVM
// (libkrun) via the microsandbox runtime.
type ProviderMicrosandboxDriverConfig struct {
	// Memory is the guest memory limit in MiB. Empty uses the runtime default.
	Memory string `json:"memory,omitempty"`

	// CPUs is the number of virtual CPUs. Empty uses the runtime default.
	CPUs string `json:"cpus,omitempty"`

	// MaxMemory is the hotplug memory ceiling in MiB. Empty uses the default.
	MaxMemory string `json:"maxMemory,omitempty"`

	// MaxCPUs is the hotplug CPU ceiling. Empty uses the default.
	MaxCPUs string `json:"maxCpus,omitempty"`

	// BlockEgress denies outbound public network (sandbox hardening).
	BlockEgress types.StrBool `json:"blockEgress,omitempty"`

	// Ephemeral removes the sandbox's disk state when it stops.
	Ephemeral types.StrBool `json:"ephemeral,omitempty"`

	// Storage is the OCI root disk size in GiB. Empty uses the runtime default.
	Storage string `json:"storage,omitempty"`
}

type ProviderCustomDriverConfig struct {
	// FindDevContainer is used to find an existing devcontainer
	FindDevContainer types.StrArray `json:"findDevContainer,omitempty"`

	// CommandDevContainer is used to execute a command in the devcontainer
	CommandDevContainer types.StrArray `json:"commandDevContainer,omitempty"`

	// TargetArchitecture is used to find out the target architecture
	TargetArchitecture types.StrArray `json:"targetArchitecture,omitempty"`

	// RunDevContainer is used to actually run the devcontainer
	RunDevContainer types.StrArray `json:"runDevContainer,omitempty"`

	// StartDevContainer is used to start the devcontainer
	StartDevContainer types.StrArray `json:"startDevContainer,omitempty"`

	// StopDevContainer is used to stop the devcontainer
	StopDevContainer types.StrArray `json:"stopDevContainer,omitempty"`

	// DeleteDevContainer is used to delete the devcontainer
	DeleteDevContainer types.StrArray `json:"deleteDevContainer,omitempty"`

	// CanReprovision returns true if the driver can reprovision the devcontainer
	CanReprovision types.StrBool `json:"canReprovision,omitempty"`

	// GetDevContainerLogs returns the logs of the devcontainer
	GetDevContainerLogs types.StrArray `json:"getDevContainerLogs,omitempty"`
}

type ProviderDockerDriverConfig struct {
	// Path where to find the docker binary, defaults to 'docker'
	Path string `json:"path,omitempty"`

	// If false, Devsy will not try to install docker into the machine.
	Install types.StrBool `json:"install,omitempty"`

	// Builder to use with docker
	Builder string `json:"builder,omitempty"`

	// Environment variables to set when running docker commands
	Env map[string]string `json:"env,omitempty"`

	// HelperImage overrides the helper image for volume operations. Empty falls
	// back to DEVSY_HELPER_IMAGE, then config.DefaultHelperImage.
	HelperImage string `json:"helperImage,omitempty"`

	// Runtime identifies the container runtime explicitly (docker, podman, nerdctl).
	// When empty, the runtime is auto-detected from the binary at Path.
	Runtime string `json:"runtime,omitempty"`

	// Elevation optionally runs docker commands through a privilege-elevation
	// helper for rootful daemons whose socket is not accessible to the current
	// user. One of "" / "none" (disabled), "pkexec", "sudo", or "doas".
	Elevation string `json:"elevation,omitempty"`
}

type ProviderKubernetesDriverConfig struct {
	KubernetesContext   string `json:"kubernetesContext,omitempty"`
	KubernetesConfig    string `json:"kubernetesConfig,omitempty"`
	KubernetesNamespace string `json:"kubernetesNamespace,omitempty"`
	PodTimeout          string `json:"podTimeout,omitempty"`

	KubernetesPullSecretsEnabled string `json:"kubernetesPullSecretsEnabled,omitempty"`
	CreateNamespace              string `json:"createNamespace,omitempty"`
	ClusterRole                  string `json:"clusterRole,omitempty"`
	ServiceAccount               string `json:"serviceAccount,omitempty"`

	Architecture      string `json:"architecture,omitempty"`
	InactivityTimeout string `json:"inactivityTimeout,omitempty"`
	StorageClass      string `json:"storageClass,omitempty"`

	DiskSize             string `json:"diskSize,omitempty"`
	PvcAccessMode        string `json:"pvcAccessMode,omitempty"`
	PvcAnnotations       string `json:"pvcAnnotations,omitempty"`
	NodeSelector         string `json:"nodeSelector,omitempty"`
	Resources            string `json:"resources,omitempty"`
	WorkspaceVolumeMount string `json:"workspaceVolumeMount,omitempty"`

	PodManifestTemplate string `json:"podManifestTemplate,omitempty"`
	Labels              string `json:"labels,omitempty"`

	StrictSecurity       string `json:"strictSecurity,omitempty"`
	AgentSecurityContext string `json:"agentSecurityContext,omitempty"`

	// AgentInstallPath overrides where the agent binary is installed inside
	// the devsy/devsy-init containers. Defaults to /usr/local/bin/devsy,
	// which requires root to write; set this (to a path under a writable
	// mount, e.g. the workspace volume) when running non-root so delivery
	// and the container's own entrypoint agree on a writable location.
	AgentInstallPath string `json:"agentInstallPath,omitempty"`

	// KubernetesUserNamespaces opts into setting spec.hostUsers to false
	// (unless a pod template already sets it), so the kubelet maps the
	// container's UIDs into a Linux user namespace instead of the host's.
	// Defaults unset: STRICT_SECURITY and AGENT_SECURITY_CONTEXT also set
	// HostUsers false for OpenShift restricted-v3 compatibility. The field's
	// mere presence requires the cluster's UserNamespacesSupport feature gate
	// (on by default only from Kubernetes 1.33) and node-level user-namespace
	// support (Linux kernel 6.3+, containerd 2.0+/CRI-O 1.25+); use a pod
	// template to override HostUsers on clusters without that support.
	KubernetesUserNamespaces string `json:"kubernetesUserNamespaces,omitempty"`
}

type ProviderAgentConfigExec struct {
	// Shutdown is the remote command to run when the remote machine
	// should shutdown.
	Shutdown types.StrArray `json:"shutdown,omitempty"`
}

type ProviderBinary struct {
	// The current OS
	OS string `json:"os,omitempty"`

	// The current Arch
	Arch string `json:"arch,omitempty"`

	// Checksum is the sha256 hash of the binary
	Checksum string `json:"checksum,omitempty"`

	// Path is the binary url to download from or relative path to use
	Path string `json:"path,omitempty"`

	// ArchivePath is the path within the archive to extract
	ArchivePath string `json:"archivePath,omitempty"`

	// Name is the name of the binary to store locally
	Name string `json:"name,omitempty"`
}

type ProviderCommands struct {
	// Init is run directly after `devsy provider init`
	Init types.StrArray `json:"init,omitempty"`

	// Command executes a command on the server
	Command types.StrArray `json:"command,omitempty"`

	// Create creates a new server
	Create types.StrArray `json:"create,omitempty"`

	// Delete destroys a server
	Delete types.StrArray `json:"delete,omitempty"`

	// Start starts a stopped server
	Start types.StrArray `json:"start,omitempty"`

	// Stop stops a running server
	Stop types.StrArray `json:"stop,omitempty"`

	// Status retrieves the server status
	Status types.StrArray `json:"status,omitempty"`

	// Describe retrieves the server description
	Describe types.StrArray `json:"describe,omitempty"`

	// Proxy proxies commands
	Proxy *ProxyCommands `json:"proxy,omitempty"`

	// Daemon commands
	Daemon *DaemonCommands `json:"daemon,omitempty"`
}

type ProxyCommands struct {
	// Up proxies the up command
	Up types.StrArray `json:"up,omitempty"`

	// Stop proxies the stop command
	Stop types.StrArray `json:"stop,omitempty"`

	// Delete proxies the delete command
	Delete types.StrArray `json:"delete,omitempty"`

	// Ssh proxies the ssh command
	Ssh types.StrArray `json:"ssh,omitempty"`

	// Status proxies the status command
	Status types.StrArray `json:"status,omitempty"`

	// Health checks the health of the platform
	Health types.StrArray `json:"health,omitempty"`

	// Create creates entities associated with this provider
	Create CreateProxyCommands `json:"create"`

	// Get gets entities associated with this provider
	Get GetProxyCommands `json:"get"`

	// List lists all entities associated with this provider
	List ListProxyCommands `json:"list"`

	// Watch lists all entities associated with this provider and then watches for changes
	Watch WatchProxyCommands `json:"watch"`

	// Update updates entities associated with this provider
	Update UpdateProxyCommands `json:"update"`
}

type ListProxyCommands struct {
	// Workspaces lists all workspaces
	Workspaces types.StrArray `json:"workspaces,omitempty"`

	// Projects lists all projects
	Projects types.StrArray `json:"projects,omitempty"`

	// Templates lists all templates in a project
	Templates types.StrArray `json:"templates,omitempty"`

	// Clusters lists all clusters and runners in a project
	Clusters types.StrArray `json:"clusters,omitempty"`
}

type WatchProxyCommands struct {
	// Workspaces watches all workspaces and updates on changes
	Workspaces types.StrArray `json:"workspaces,omitempty"`
}

type CreateProxyCommands struct {
	// Workspace creates a workspace instance
	Workspace types.StrArray `json:"workspace,omitempty"`
}

type GetProxyCommands struct {
	// Workspace gets a workspace instance
	Workspace types.StrArray `json:"workspace,omitempty"`

	// Self gets self for this provider
	Self types.StrArray `json:"self,omitempty"`

	// Version gets the for this pro instance
	Version types.StrArray `json:"version,omitempty"`
}

type UpdateProxyCommands struct {
	// Workspace updates a workspace instance
	Workspace types.StrArray `json:"workspace,omitempty"`
}

type DaemonCommands struct {
	// Start starts the daemon
	Start types.StrArray `json:"start,omitempty"`
	// Status gets the daemon status
	Status types.StrArray `json:"status,omitempty"`
}

type SubOptions struct {
	Options map[string]*types.Option `json:"options,omitempty"`
}

func (c *ProviderConfig) IsMachineProvider() bool {
	return len(c.Exec.Create) > 0
}

func (c *ProviderConfig) IsProxyProvider() bool {
	return c.Exec.Proxy != nil
}

func (c *ProviderConfig) HasHealthCheck() bool {
	return c.Exec.Proxy != nil && len(c.Exec.Proxy.Health) > 0
}

func (c *ProviderConfig) IsDaemonProvider() bool {
	return c.Exec.Daemon != nil
}
