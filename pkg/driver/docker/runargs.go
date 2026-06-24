package docker

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/ide/jetbrains"
	"github.com/devsy-org/devsy/pkg/log"
)

// runArgsBuilder accumulates the argument list for `docker run`. Each step
// mutates args and returns an error so the build loop can short-circuit
// uniformly, regardless of whether the step can actually fail.
type runArgsBuilder struct {
	args   []string
	driver *dockerDriver
	params *driver.RunDockerDevContainerParams
	helper *docker.DockerHelper
}

func (d *dockerDriver) buildRunArgs(
	params *driver.RunDockerDevContainerParams,
	helper *docker.DockerHelper,
) ([]string, error) {
	b := &runArgsBuilder{
		args:   []string{"run"},
		driver: d,
		params: params,
		helper: helper,
	}

	if helper.GetRuntime().SupportsSignalProxy() {
		b.args = append(b.args, "--sig-proxy=false")
	}

	steps := []func() error{
		b.addPorts,
		b.addWorkspaceMount,
		b.addUser,
		b.addEnv,
		b.addInit,
		b.addPrivileged,
		b.addPodmanArgs,
		b.addCapabilities,
		b.addMounts,
		b.addIDEMount,
		b.addLabels,
		b.addGPU,
		b.addRunArgs,
		b.addRunPlatform,
		b.addDetached,
		b.addEntrypoint,
		b.addImage,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}

	return b.args, nil
}

func (b *runArgsBuilder) addPorts() error {
	b.args = b.driver.addPortArgs(b.args, b.params.ParsedConfig)
	return nil
}

func (b *runArgsBuilder) addWorkspaceMount() error {
	b.args = b.driver.addWorkspaceMountArgs(b.args, b.params.Options, b.helper)
	return nil
}

func (b *runArgsBuilder) addUser() error {
	b.args = b.driver.addUserArgs(b.args, b.params.Options)
	return nil
}

func (b *runArgsBuilder) addEnv() error {
	b.args = b.driver.addEnvArgs(b.args, b.params.Options)
	return nil
}

func (b *runArgsBuilder) addInit() error {
	b.args = b.driver.addInitArgs(b.args, b.params.Options)
	return nil
}

func (b *runArgsBuilder) addPrivileged() error {
	b.args = b.driver.addPrivilegedArgs(b.args, b.params.Options)
	return nil
}

func (b *runArgsBuilder) addPodmanArgs() error {
	b.args = append(b.args, b.driver.getPodmanArgs(b.params.Options, b.params.ParsedConfig)...)
	return nil
}

func (b *runArgsBuilder) addCapabilities() error {
	b.args = b.driver.addCapabilityArgs(b.args, b.params.Options)
	return nil
}

func (b *runArgsBuilder) addMounts() error {
	args, err := b.driver.addMountArgs(b.args, b.params.Options)
	if err != nil {
		return err
	}
	b.args = args
	return nil
}

func (b *runArgsBuilder) addIDEMount() error {
	b.args = b.driver.addIDEMountArgs(b.args, b.params.IDE, b.params.IDEOptions)
	return nil
}

func (b *runArgsBuilder) addLabels() error {
	b.args = b.driver.addLabelArgs(b.args, b.params.WorkspaceID, b.params.Options)
	return nil
}

func (b *runArgsBuilder) addGPU() error {
	b.args = appendGPUOptions(b.params.ParsedConfig, b.driver, b.args, b.params.GPUAvailability)
	return nil
}

func (b *runArgsBuilder) addRunArgs() error {
	b.args = append(b.args, b.params.ParsedConfig.RunArgs...)
	return nil
}

func (b *runArgsBuilder) addRunPlatform() error {
	if b.params.Options.Platform == "" {
		return nil
	}
	for _, a := range b.params.ParsedConfig.RunArgs {
		if a == "--platform" || strings.HasPrefix(a, "--platform=") {
			return nil // explicit config wins; avoid duplicate/conflict.
		}
	}
	b.args = append(b.args, "--platform="+b.params.Options.Platform)
	return nil
}

func (b *runArgsBuilder) addDetached() error {
	b.args = append(b.args, "-d")
	return nil
}

func (b *runArgsBuilder) addEntrypoint() error {
	b.args = b.driver.addEntrypointArgs(b.args, b.params.Options)
	return nil
}

func (b *runArgsBuilder) addImage() error {
	b.args = append(b.args, b.params.Options.Image)
	b.args = append(b.args, b.params.Options.Cmd...)
	return nil
}

func (d *dockerDriver) addPortArgs(
	args []string,
	parsedConfig *config.DevContainerConfig,
) []string {
	for _, appPort := range parsedConfig.AppPort {
		intPort, err := strconv.Atoi(appPort)
		if err != nil {
			args = append(args, "-p", appPort)
		} else {
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", intPort, intPort))
		}
	}
	return args
}

func (d *dockerDriver) addWorkspaceMountArgs(
	args []string,
	options *driver.RunOptions,
	helper *docker.DockerHelper,
) []string {
	if options.WorkspaceMount != nil {
		workspacePath := d.EnsurePath(options.WorkspaceMount)
		mountPath := workspacePath.String()
		if !helper.GetRuntime().SupportsMountConsistency() {
			mountPath = stripMountConsistency(mountPath)
		}
		if shouldBustBindCache(helper) {
			mountPath = withBindCreateSrc(mountPath)
		}
		args = append(args, "--mount", mountPath)
	}
	return args
}

// minBindCreateSrcMajor is the docker CLI major version that added the
// bind-create-src mount option; older clients reject it at parse time.
const minBindCreateSrcMajor = 29

// shouldBustBindCache reports whether to add bind-create-src to the workspace
// mount. Gated to Docker (Podman/nerdctl reject it), Docker Desktop
// (GOOS != linux), and CLI >= v29.
func shouldBustBindCache(helper *docker.DockerHelper) bool {
	if helper.GetRuntime().Name() != docker.RuntimeDocker {
		return false
	}
	if runtime.GOOS == "linux" {
		return false
	}
	return dockerMajorAtLeast(helper.ClientVersion(context.Background()), minBindCreateSrcMajor)
}

// dockerMajorAtLeast reports whether version's major component is >= minMajor.
// An unparseable version returns false.
func dockerMajorAtLeast(version string, minMajor int) bool {
	major, _, ok := strings.Cut(version, ".")
	if !ok {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(major))
	if err != nil {
		return false
	}
	return n >= minMajor
}

// withBindCreateSrc adds bind-create-src=true to a bind --mount whose source
// exists, forcing Docker Desktop to re-resolve a stale-cached path. A missing
// source is left untouched so docker still fails loudly rather than binding an
// empty dir.
func withBindCreateSrc(mountPath string) string {
	m := config.ParseMount(mountPath)
	if m.Type != "bind" || m.Source == "" {
		return mountPath
	}
	for part := range strings.SplitSeq(mountPath, ",") {
		if strings.HasPrefix(part, "bind-create-src=") {
			return mountPath
		}
	}
	if _, err := os.Stat(m.Source); err != nil {
		return mountPath
	}
	return mountPath + ",bind-create-src=true"
}

func stripMountConsistency(mount string) string {
	var parts []string
	for part := range strings.SplitSeq(mount, ",") {
		if !strings.HasPrefix(part, "consistency=") {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ",")
}

func (d *dockerDriver) addUserArgs(args []string, options *driver.RunOptions) []string {
	if options.User != "" {
		args = append(args, "-u", options.User)
	}
	return args
}

func (d *dockerDriver) addEnvArgs(args []string, options *driver.RunOptions) []string {
	for k, v := range options.Env {
		args = append(args, "-e", k+"="+v)
	}
	return args
}

func (d *dockerDriver) addInitArgs(args []string, options *driver.RunOptions) []string {
	if options.Init != nil && *options.Init {
		args = append(args, "--init")
	}
	return args
}

func (d *dockerDriver) addPrivilegedArgs(args []string, options *driver.RunOptions) []string {
	if options.Privileged != nil && *options.Privileged {
		args = append(args, "--privileged")
	}
	return args
}

func (d *dockerDriver) addCapabilityArgs(args []string, options *driver.RunOptions) []string {
	for _, capAdd := range options.CapAdd {
		args = append(args, "--cap-add", capAdd)
	}
	for _, securityOpt := range options.SecurityOpt {
		args = append(args, "--security-opt", securityOpt)
	}
	return args
}

func (d *dockerDriver) addMountArgs(args []string, options *driver.RunOptions) ([]string, error) {
	for _, mount := range options.Mounts {
		if mount.Type == "bind" && mount.Source != "" {
			if _, err := os.Stat(mount.Source); os.IsNotExist(err) {
				return nil, fmt.Errorf("bind mount source path does not exist %s", mount.Source)
			}
		}
		args = append(args, "--mount", mount.String())
	}
	return args, nil
}

// ideVolumeProviders maps each supported JetBrains IDE to its server
// constructor. Every constructor shares the same signature and returns a
// *jetbrains.GenericJetBrainsServer, so the IDE mount is a single table lookup
// rather than a per-IDE switch.
var ideVolumeProviders = map[string]func(string, map[string]pkgconfig.OptionValue) *jetbrains.GenericJetBrainsServer{
	string(pkgconfig.IDEGoland):    jetbrains.NewGolandServer,
	string(pkgconfig.IDERustRover): jetbrains.NewRustRoverServer,
	string(pkgconfig.IDEPyCharm):   jetbrains.NewPyCharmServer,
	string(pkgconfig.IDEPhpStorm):  jetbrains.NewPhpStorm,
	string(pkgconfig.IDEIntellij):  jetbrains.NewIntellij,
	string(pkgconfig.IDECLion):     jetbrains.NewCLionServer,
	string(pkgconfig.IDERider):     jetbrains.NewRiderServer,
	string(pkgconfig.IDERubyMine):  jetbrains.NewRubyMineServer,
	string(pkgconfig.IDEWebStorm):  jetbrains.NewWebStormServer,
	string(pkgconfig.IDEDataSpell): jetbrains.NewDataSpellServer,
}

func (d *dockerDriver) addIDEMountArgs(
	args []string,
	ide string,
	ideOptions map[string]pkgconfig.OptionValue,
) []string {
	if newServer, ok := ideVolumeProviders[ide]; ok {
		args = append(args, "--mount", newServer("", ideOptions).GetVolume())
	}
	return args
}

func (d *dockerDriver) addLabelArgs(
	args []string,
	workspaceId string,
	options *driver.RunOptions,
) []string {
	labels := append(config.GetIDLabels(workspaceId, d.IDLabels), options.Labels...)
	for _, label := range labels {
		args = append(args, "-l", label)
	}
	return args
}

func (d *dockerDriver) addEntrypointArgs(args []string, options *driver.RunOptions) []string {
	if options.Entrypoint != "" {
		args = append(args, "--entrypoint", options.Entrypoint)
	}
	return args
}

func resolveGPUAvailability(override string, d *dockerDriver) bool {
	switch override {
	case "true":
		return true
	case "false":
		return false
	default:
		available, _ := d.Docker.GPUSupportEnabled()
		return available
	}
}

func appendGPUOptions(
	parsedConfig *config.DevContainerConfig,
	d *dockerDriver,
	args []string,
	gpuAvailabilityOverride string,
) []string {
	if parsedConfig.HostRequirements != nil {
		gpuAvailable := resolveGPUAvailability(gpuAvailabilityOverride, d)
		enableGPU, warnIfMissing := parsedConfig.HostRequirements.ShouldEnableGPU(gpuAvailable)
		if enableGPU {
			args = append(args, "--gpus", "all")
		}
		if warnIfMissing {
			log.Warn("GPU required but not available on host")
		}
	}
	return args
}

func (d *dockerDriver) getPodmanArgs(
	options *driver.RunOptions,
	parsedConfig *config.DevContainerConfig,
) []string {
	if !d.Docker.GetRuntime().NeedsUserNamespaceArgs() {
		return []string{}
	}

	var args []string
	args = d.addUsernsArgs(args, options)
	args = d.addIdMappingArgs(args, options)
	args = d.addKeepIdArgs(args, options, parsedConfig)
	return args
}

func (d *dockerDriver) addUsernsArgs(args []string, options *driver.RunOptions) []string {
	if options.Userns != "" {
		args = append(args, "--userns", options.Userns)
	}
	return args
}

func (d *dockerDriver) addIdMappingArgs(args []string, options *driver.RunOptions) []string {
	for _, uidMap := range options.UidMap {
		args = append(args, "--uidmap", uidMap)
	}
	for _, gidMap := range options.GidMap {
		args = append(args, "--gidmap", gidMap)
	}
	return args
}

func (d *dockerDriver) addKeepIdArgs(
	args []string,
	options *driver.RunOptions,
	parsedConfig *config.DevContainerConfig,
) []string {
	if d.hasIdMapping(options, parsedConfig) || options.Userns != "" {
		return args
	}

	remoteUser := d.getRemoteUser(options, parsedConfig)
	if remoteUser != rootUser && remoteUser != "0" && os.Getuid() != 0 {
		args = append(args, "--userns=keep-id")
	}
	return args
}

func (d *dockerDriver) hasIdMapping(
	options *driver.RunOptions,
	parsedConfig *config.DevContainerConfig,
) bool {
	if len(options.UidMap) > 0 || len(options.GidMap) > 0 {
		return true
	}

	if parsedConfig != nil {
		for _, arg := range parsedConfig.RunArgs {
			if strings.Contains(arg, "--uidmap") || strings.Contains(arg, "--gidmap") {
				return true
			}
		}
	}
	return false
}

func (d *dockerDriver) getRemoteUser(
	options *driver.RunOptions,
	parsedConfig *config.DevContainerConfig,
) string {
	if parsedConfig != nil {
		if parsedConfig.RemoteUser != "" {
			return parsedConfig.RemoteUser
		}
		if parsedConfig.ContainerUser != "" {
			return parsedConfig.ContainerUser
		}
	}
	if options.User != "" {
		return options.User
	}
	return rootUser
}

func (d *dockerDriver) EnsurePath(path *config.Mount) *config.Mount {
	// Local Windows to remote Linux over TCP requires manual path conversion.
	if runtime.GOOS == "windows" {
		for _, v := range d.Docker.Environment {
			// Convert only when DOCKER_HOST is a direct TCP connection to a
			// docker daemon running in WSL, not the docker-desktop engine.
			if strings.Contains(v, "DOCKER_HOST=tcp://") {
				unixPath := path.Source
				unixPath = strings.Replace(unixPath, "C:", "c", 1)
				unixPath = strings.ReplaceAll(unixPath, "\\", "/")
				unixPath = "/mnt/" + unixPath

				path.Source = unixPath

				return path
			}
		}
	}
	return path
}
