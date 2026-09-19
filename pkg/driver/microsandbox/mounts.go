package microsandbox

import (
	"fmt"
	"strings"

	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
)

type statVirtualization string

const (
	statVirtStrict  statVirtualization = "strict"
	statVirtRelaxed statVirtualization = "relaxed"
	statVirtOff     statVirtualization = "off"
)

type hostPermissions string

const (
	hostPermissionsPrivate hostPermissions = "private"
	hostPermissionsMirror  hostPermissions = "mirror"
)

type mountOwner struct {
	UID uint32
	GID uint32
}

type mountPolicy struct {
	StatVirtualization statVirtualization
	HostPermissions    hostPermissions
	Owner              *mountOwner
}

type workspaceMountPolicy struct {
	StatVirtualization statVirtualization
	HostPermissions    hostPermissions
}

func parseWorkspaceMountPolicy(
	cfg provider.ProviderMicrosandboxDriverConfig,
) (workspaceMountPolicy, error) {
	stat, err := parseStatVirtualization(cfg.WorkspaceStatVirtualization)
	if err != nil {
		return workspaceMountPolicy{}, err
	}
	host, err := parseHostPermissions(cfg.WorkspaceHostPermissions)
	if err != nil {
		return workspaceMountPolicy{}, err
	}
	policy := workspaceMountPolicy{StatVirtualization: stat, HostPermissions: host}
	if policy.StatVirtualization == statVirtOff && policy.HostPermissions == hostPermissionsMirror {
		return workspaceMountPolicy{}, fmt.Errorf(
			"invalid microsandbox workspace mount policy: host-perms=mirror requires " +
				"stat virtualization; use strict/relaxed or set host permissions to private",
		)
	}
	return policy, nil
}

func parseStatVirtualization(value string) (statVirtualization, error) {
	policy := statVirtualization(strings.TrimSpace(value))
	switch policy {
	case "":
		return statVirtStrict, nil
	case statVirtStrict, statVirtRelaxed, statVirtOff:
		return policy, nil
	default:
		return "", fmt.Errorf("invalid microsandbox workspace stat virtualization %q", policy)
	}
}

func parseHostPermissions(value string) (hostPermissions, error) {
	policy := hostPermissions(strings.TrimSpace(value))
	switch policy {
	case "":
		return hostPermissionsMirror, nil
	case hostPermissionsMirror, hostPermissionsPrivate:
		return policy, nil
	default:
		return "", fmt.Errorf("invalid microsandbox workspace host permissions %q", policy)
	}
}

func (d *microsandboxDriver) volumeMounts(options *driver.RunOptions) []volumeMount {
	var out []volumeMount
	if m := d.workspaceMount(options.WorkspaceMount); m != nil {
		out = append(out, *m)
	}
	for _, m := range options.Mounts {
		if vm, ok := toVolumeMount(m); ok {
			out = append(out, vm)
		}
	}
	return out
}

func (d *microsandboxDriver) workspaceMount(m *devcontainerconfig.Mount) *volumeMount {
	b := bindMount(m)
	if b == nil {
		return nil
	}
	b.Policy = mountPolicy{
		StatVirtualization: d.workspaceMountPolicy.StatVirtualization,
		HostPermissions:    d.workspaceMountPolicy.HostPermissions,
	}
	return b
}

func toVolumeMount(m *devcontainerconfig.Mount) (volumeMount, bool) {
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

func bindMount(m *devcontainerconfig.Mount) *volumeMount {
	if m == nil || m.Source == "" || m.Target == "" {
		return nil
	}
	return &volumeMount{Target: m.Target, Source: m.Source, ReadOnly: m.IsReadOnly()}
}

func mountOptions(m volumeMount) []string {
	var options []string
	if m.ReadOnly {
		options = append(options, "ro")
	}
	if m.Policy.StatVirtualization != "" {
		options = append(options, "stat-virt="+string(m.Policy.StatVirtualization))
	}
	if m.Policy.HostPermissions != "" {
		options = append(options, "host-perms="+string(m.Policy.HostPermissions))
	}
	if m.Policy.Owner != nil {
		options = append(
			options,
			fmt.Sprintf("uid=%d", m.Policy.Owner.UID),
			fmt.Sprintf("gid=%d", m.Policy.Owner.GID),
		)
	}
	return options
}

func bindMountSpec(m volumeMount) string {
	spec := m.Source + ":" + m.Target
	if options := mountOptions(m); len(options) > 0 {
		spec += ":" + strings.Join(options, ",")
	}
	return spec
}
