// Package microsandbox is a Devsy driver that runs devcontainers as microVMs.
package microsandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	dockerdriver "github.com/devsy-org/devsy/pkg/driver/docker"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

const userLabel = "devsy.sh/user"

const createdAtLayout = "2006-01-02T15:04:05Z07:00"

type specDefaults struct {
	memory      uint32
	cpus        uint8
	ephemeral   bool
	idleTimeout time.Duration
	maxMemory   uint32
	maxCPUs     uint8
	blockEgress bool
	rootDiskGB  uint32
}

type microsandboxDriver struct {
	client        sandboxClient
	idLabels      []string
	defaults      specDefaults
	workspaceInfo *provider.AgentWorkspaceInfo
	docker        driver.ImageDriver
}

var (
	_ driver.RunOptionsDriver     = (*microsandboxDriver)(nil)
	_ driver.ReprovisioningDriver = (*microsandboxDriver)(nil)
	_ driver.ImageDriver          = (*microsandboxDriver)(nil)
	_ driver.Preflighter          = (*microsandboxDriver)(nil)
)

// Preflight checks the microsandbox runtime binary is installed. There is no
// daemon to auto-start, so a missing binary is surfaced for the user.
func (d *microsandboxDriver) Preflight(ctx context.Context, _ driver.PreflightOptions) error {
	if err := d.client.EnsureInstalled(ctx); err != nil {
		return &driver.PreflightError{Provider: provider.MicrosandboxDriver, Err: err}
	}
	return nil
}

// CanReprovision returns false: in-place reprovision passes nil options, which
// a microVM cannot rebuild from. --recreate still works via delete+create.
func (d *microsandboxDriver) CanReprovision() bool {
	return false
}

func NewMicrosandboxDriver(
	_ context.Context,
	workspaceInfo *provider.AgentWorkspaceInfo,
) (driver.Driver, error) {
	client := cliClient{}

	cfg := workspaceInfo.Agent.Microsandbox
	defaults := specDefaults{
		memory:      parseUint32(cfg.Memory),
		cpus:        parseUint8(cfg.CPUs),
		ephemeral:   cfg.Ephemeral == pkgconfig.BoolTrue,
		idleTimeout: parseDuration(workspaceInfo.Agent.ContainerTimeout),
		maxMemory:   parseUint32(cfg.MaxMemory),
		maxCPUs:     parseUint8(cfg.MaxCPUs),
		blockEgress: cfg.BlockEgress == pkgconfig.BoolTrue,
		rootDiskGB:  parseUint32(cfg.Storage),
	}

	log.Debugf(
		"using microsandbox driver: memory=%dMiB cpus=%d ephemeral=%t idleTimeout=%s",
		defaults.memory, defaults.cpus, defaults.ephemeral, defaults.idleTimeout,
	)
	d := newDriver(client, workspaceInfo.CLIOptions.IDLabels, defaults)
	d.workspaceInfo = workspaceInfo
	return d, nil
}

func newDriver(client sandboxClient, idLabels []string, defaults specDefaults) *microsandboxDriver {
	return &microsandboxDriver{client: client, idLabels: idLabels, defaults: defaults}
}

func (d *microsandboxDriver) RunDevContainer(
	ctx context.Context,
	workspaceID string,
	options *driver.RunOptions,
) error {
	return d.runFromOptions(ctx, workspaceID, options, nil)
}

func (d *microsandboxDriver) RunImageDevContainer(
	ctx context.Context,
	params *driver.RunImageDevContainerParams,
) error {
	if err := checkGPURequirement(params.ParsedConfig); err != nil {
		return err
	}
	var hostReqs *config.HostRequirements
	if params.ParsedConfig != nil {
		hostReqs = params.ParsedConfig.HostRequirements
	}
	return d.runFromOptions(ctx, params.WorkspaceID, params.Options, hostReqs)
}

func checkGPURequirement(parsedConfig *config.DevContainerConfig) error {
	if parsedConfig == nil ||
		parsedConfig.HostRequirements == nil ||
		parsedConfig.HostRequirements.GPU == nil {
		return nil
	}
	switch parsedConfig.HostRequirements.GPU.Value {
	case "true":
		return fmt.Errorf(
			"microsandbox does not support GPU passthrough, but hostRequirements.gpu is required; " +
				"use a GPU-capable provider or set gpu to \"optional\"",
		)
	case "optional":
		log.Warnf(
			"hostRequirements.gpu is optional; microsandbox provides no GPU, continuing without one",
		)
	}
	return nil
}

func (d *microsandboxDriver) FindDevContainer(
	ctx context.Context,
	workspaceID string,
) (*config.ContainerDetails, error) {
	info, err := d.client.Find(ctx, sandboxName(workspaceID))
	if err != nil || info == nil {
		return nil, err
	}
	return toContainerDetails(info), nil
}

func (d *microsandboxDriver) CommandDevContainer(
	ctx context.Context,
	params *driver.CommandParams,
) error {
	return d.client.Exec(ctx, sandboxName(params.WorkspaceID), execRequest{
		Command: params.Command,
		User:    params.User,
		Stdin:   params.Stdin,
		Stdout:  params.Stdout,
		Stderr:  params.Stderr,
	})
}

// CommandContainerArgv satisfies the podExecCapableDriver interface that
// pkg/devcontainer detects to stream the agent binary in over stdin.
func (d *microsandboxDriver) CommandContainerArgv(
	ctx context.Context,
	workspaceID string,
	argv []string,
	streams driver.Streams,
) error {
	return d.client.Exec(ctx, sandboxName(workspaceID), execRequest{
		Argv:   argv,
		User:   "root",
		Stdin:  streams.Stdin,
		Stdout: streams.Stdout,
		Stderr: streams.Stderr,
	})
}

func (d *microsandboxDriver) TargetArchitecture(
	ctx context.Context,
	workspaceID string,
) (string, error) {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return runtime.GOARCH, nil
	default:
		return "", fmt.Errorf("unsupported architecture %q for microsandbox", runtime.GOARCH)
	}
}

func (d *microsandboxDriver) InspectImage(
	ctx context.Context,
	imageName string,
) (*config.ImageDetails, error) {
	arch, err := d.TargetArchitecture(ctx, "")
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.GetImageConfigForArch(ctx, imageName, arch)
	if err != nil {
		return nil, fmt.Errorf("get image config for %s: %w", imageName, err)
	}
	return &config.ImageDetails{
		ID: imageName,
		Config: config.ImageDetailsConfig{
			User:       cfg.Config.User,
			Env:        cfg.Config.Env,
			Labels:     cfg.Config.Labels,
			Entrypoint: cfg.Config.Entrypoint,
			Cmd:        cfg.Config.Cmd,
		},
	}, nil
}

func (d *microsandboxDriver) GetImageTag(_ context.Context, imageName string) (string, error) {
	return imageName, nil
}

func (d *microsandboxDriver) BuildDevContainer(
	ctx context.Context,
	req driver.BuildRequest,
) (*config.BuildInfo, error) {
	dockerDriver, err := d.dockerImageDriver()
	if err != nil {
		return nil, err
	}
	return dockerDriver.BuildDevContainer(ctx, req)
}

func (d *microsandboxDriver) PushDevContainer(ctx context.Context, image string) error {
	dockerDriver, err := d.dockerImageDriver()
	if err != nil {
		return err
	}
	return dockerDriver.PushDevContainer(ctx, image)
}

func (d *microsandboxDriver) TagDevContainer(ctx context.Context, image, tag string) error {
	dockerDriver, err := d.dockerImageDriver()
	if err != nil {
		return err
	}
	return dockerDriver.TagDevContainer(ctx, image, tag)
}

func (d *microsandboxDriver) UpdateContainerUserUID(
	_ context.Context,
	_ string,
	_ *config.DevContainerConfig,
	_ io.Writer,
) error {
	return nil
}

// RequiresWorkspaceChown reports that the virtiofs workspace share is
// root-owned in the guest, so the agent must chown it to the remote user.
func (d *microsandboxDriver) RequiresWorkspaceChown() bool {
	return true
}

func (d *microsandboxDriver) StartDevContainer(ctx context.Context, workspaceID string) error {
	if err := d.client.Start(ctx, sandboxName(workspaceID)); err != nil {
		return fmt.Errorf("start microsandbox VM: %w", err)
	}
	return nil
}

func (d *microsandboxDriver) StopDevContainer(ctx context.Context, workspaceID string) error {
	if err := d.client.Stop(ctx, sandboxName(workspaceID)); err != nil {
		return fmt.Errorf("stop microsandbox VM: %w", err)
	}
	return nil
}

// DeleteDevContainer stops before removing; the runtime only removes stopped
// sandboxes.
func (d *microsandboxDriver) DeleteDevContainer(ctx context.Context, workspaceID string) error {
	name := sandboxName(workspaceID)
	info, err := d.client.Find(ctx, name)
	if err != nil {
		return err
	}
	if info == nil {
		return nil
	}
	if info.Running {
		if err := d.client.Stop(ctx, name); err != nil {
			return fmt.Errorf("stop microsandbox VM before delete: %w", err)
		}
	}
	if err := d.client.Remove(ctx, name); err != nil {
		return fmt.Errorf("remove microsandbox VM: %w", err)
	}
	return nil
}

func (d *microsandboxDriver) GetDevContainerLogs(
	ctx context.Context,
	workspaceID string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return d.client.Logs(ctx, sandboxName(workspaceID), stdout)
}

func (d *microsandboxDriver) runFromOptions(
	ctx context.Context,
	workspaceID string,
	options *driver.RunOptions,
	hostReqs *config.HostRequirements,
) error {
	if options == nil {
		return fmt.Errorf(
			"microsandbox driver requires run options (in-place reprovision is unsupported)",
		)
	}
	if options.Image == "" {
		return fmt.Errorf("microsandbox driver requires an image to run")
	}
	warnUnsupportedOptions(options)
	if err := d.DeleteDevContainer(ctx, workspaceID); err != nil {
		return fmt.Errorf("clean stale microsandbox VM before create: %w", err)
	}
	if err := d.client.EnsureImage(ctx, options.Image); err != nil {
		log.Debugf("microsandbox ensure image %q failed (continuing): %v", options.Image, err)
	}
	if err := d.client.Create(
		ctx,
		sandboxName(workspaceID),
		d.buildSpec(workspaceID, options, hostReqs),
	); err != nil {
		return fmt.Errorf("create microsandbox VM: %w", err)
	}
	return nil
}

func (d *microsandboxDriver) dockerImageDriver() (driver.ImageDriver, error) {
	if d.docker != nil {
		return d.docker, nil
	}
	if d.workspaceInfo == nil {
		return nil, fmt.Errorf("microsandbox build requires workspace info")
	}
	dd, err := dockerdriver.NewDockerDriver(d.workspaceInfo)
	if err != nil {
		return nil, fmt.Errorf("microsandbox needs docker to build this devcontainer: %w", err)
	}
	d.docker = dd
	return dd, nil
}

// buildSpec resolves sizing from, in priority order, the operator-configured
// MICROSANDBOX_* defaults, then the devcontainer's hostRequirements, falling
// back to the microsandbox runtime default (zero) when neither is set.
func (d *microsandboxDriver) buildSpec(
	workspaceID string, options *driver.RunOptions, hostReqs *config.HostRequirements,
) sandboxSpec {
	labels := config.ListToObject(config.GetIDLabels(workspaceID, d.idLabels))
	if labels == nil {
		labels = map[string]string{}
	}
	if options.User != "" {
		labels[userLabel] = options.User
	}
	memory := d.defaults.memory
	if memory == 0 {
		memory = hostRequirementMemoryMiB(hostReqs)
	}
	cpus := d.defaults.cpus
	if cpus == 0 {
		cpus = hostRequirementCPUs(hostReqs)
	}
	rootDiskGB := d.defaults.rootDiskGB
	if rootDiskGB == 0 {
		rootDiskGB = hostRequirementStorageGB(hostReqs)
	}
	return sandboxSpec{
		Image:       options.Image,
		Entrypoint:  options.Entrypoint,
		Cmd:         options.Cmd,
		Memory:      memory,
		CPUs:        cpus,
		Env:         options.Env,
		Labels:      labels,
		Ephemeral:   d.defaults.ephemeral,
		IdleTimeout: d.defaults.idleTimeout,
		Mounts:      volumeMounts(options),
		MaxMemory:   d.defaults.maxMemory,
		MaxCPUs:     d.defaults.maxCPUs,
		BlockEgress: d.defaults.blockEgress,
		RootDiskGB:  rootDiskGB,
	}
}

func volumeMounts(options *driver.RunOptions) []volumeMount {
	var out []volumeMount
	if b := bindMount(options.WorkspaceMount); b != nil {
		out = append(out, *b)
	}
	for _, m := range options.Mounts {
		if vm, ok := toVolumeMount(m); ok {
			out = append(out, vm)
		}
	}
	return out
}

func toVolumeMount(m *config.Mount) (volumeMount, bool) {
	if m == nil || m.Target == "" {
		return volumeMount{}, false
	}
	switch m.Type {
	case driver.MountTypeBind:
		if b := bindMount(m); b != nil {
			return *b, true
		}
	case driver.MountTypeVolume:
		return volumeMount{Target: m.Target, Volume: m.Source}, true
	case driver.MountTypeTmpfs:
		return volumeMount{Target: m.Target, Tmpfs: true}, true
	}
	return volumeMount{}, false
}

func bindMount(m *config.Mount) *volumeMount {
	if m == nil || m.Source == "" || m.Target == "" {
		return nil
	}
	return &volumeMount{Target: m.Target, Source: m.Source, ReadOnly: m.IsReadOnly()}
}

func warnUnsupportedOptions(options *driver.RunOptions) {
	var ignored []string
	if isPrivileged(options) {
		ignored = append(ignored, "privileged")
	}
	if len(options.CapAdd) > 0 {
		ignored = append(ignored, "capAdd")
	}
	if len(options.SecurityOpt) > 0 {
		ignored = append(ignored, "securityOpt")
	}
	if hasUserNSMapping(options) {
		ignored = append(ignored, "user-namespace mapping")
	}
	if len(ignored) > 0 {
		log.Warnf(
			"microsandbox ignores container-runtime options not applicable to a microVM: %s",
			strings.Join(ignored, ", "),
		)
	}
}

func isPrivileged(options *driver.RunOptions) bool {
	return options.Privileged != nil && *options.Privileged
}

func hasUserNSMapping(options *driver.RunOptions) bool {
	return options.Userns != "" || len(options.UidMap) > 0 || len(options.GidMap) > 0
}

func toContainerDetails(info *sandboxInfo) *config.ContainerDetails {
	status := "exited"
	if info.Running {
		status = "running"
	}
	return &config.ContainerDetails{
		ID:      info.Name,
		Created: info.CreatedAt.Format(createdAtLayout),
		State:   config.ContainerDetailsState{Status: status},
		Config: config.ContainerDetailsConfig{
			Labels: info.Labels,
			User:   info.Labels[userLabel],
		},
	}
}

const maxSandboxNameLen = 128

func sandboxName(workspaceID string) string {
	name := "devsy-" + workspaceID
	if len(name) <= maxSandboxNameLen {
		return name
	}
	sum := sha256.Sum256([]byte(workspaceID))
	suffix := "-" + hex.EncodeToString(sum[:])[:12]
	return name[:maxSandboxNameLen-len(suffix)] + suffix
}

func parseUint32(s string) uint32 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		log.Warnf("invalid microsandbox numeric value %q, using runtime default", s)
		return 0
	}
	return uint32(v)
}

func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		log.Warnf("invalid microsandbox inactivity timeout %q, disabling idle shutdown", s)
		return 0
	}
	return d
}

func parseUint8(s string) uint8 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		log.Warnf("invalid microsandbox numeric value %q, using runtime default", s)
		return 0
	}
	return uint8(v)
}

// hostRequirementCPUs converts devcontainer.json's hostRequirements.cpus into
// a vCPU count, used only as a fallback when no MICROSANDBOX_CPUS default is
// configured.
func hostRequirementCPUs(hostReqs *config.HostRequirements) uint8 {
	if hostReqs == nil || hostReqs.CPUs <= 0 {
		return 0
	}
	return parseUint8(strconv.Itoa(hostReqs.CPUs))
}

// hostRequirementMemoryMiB converts devcontainer.json's hostRequirements.memory
// (e.g. "8gb") into MiB, used only as a fallback when no MICROSANDBOX_MEMORY
// default is configured.
func hostRequirementMemoryMiB(hostReqs *config.HostRequirements) uint32 {
	if hostReqs == nil || hostReqs.Memory == "" {
		return 0
	}
	bytes, err := config.ParseSizeToBytes(hostReqs.Memory)
	if err != nil {
		log.Warnf(
			"invalid hostRequirements.memory %q, ignoring for microsandbox sizing: %v",
			hostReqs.Memory, err,
		)
		return 0
	}
	return ceilBytesToUint32(bytes, 1024*1024)
}

// hostRequirementStorageGB converts devcontainer.json's hostRequirements.storage
// (e.g. "32gb") into GiB for --root-disk, used only as a fallback when no
// MICROSANDBOX_STORAGE default is configured.
func hostRequirementStorageGB(hostReqs *config.HostRequirements) uint32 {
	if hostReqs == nil || hostReqs.Storage == "" {
		return 0
	}
	bytes, err := config.ParseSizeToBytes(hostReqs.Storage)
	if err != nil {
		log.Warnf(
			"invalid hostRequirements.storage %q, ignoring for microsandbox sizing: %v",
			hostReqs.Storage, err,
		)
		return 0
	}
	return ceilBytesToUint32(bytes, 1024*1024*1024)
}

// clampUint64ToUint32 saturates rather than wraps, so an outsized
// hostRequirements value degrades to the largest representable size instead
// of silently overflowing to a small or negative one.
func clampUint64ToUint32(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// ceilBytesToUint32 rounds a byte count up to the next whole unit before
// clamping.
func ceilBytesToUint32(bytes, unit uint64) uint32 {
	value := bytes / unit
	if bytes%unit != 0 {
		value++
	}
	return clampUint64ToUint32(value)
}
