package external

import (
	"slices"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
)

var (
	_ driver.ImageRunner          = (*Host)(nil)
	_ driver.ImageRunValidator    = (*Host)(nil)
	_ driver.MountCapableDriver   = (*Host)(nil)
	_ driver.MountDeliveryDriver  = (*Host)(nil)
	_ driver.WorkspaceChowner     = (*Host)(nil)
	_ driver.RecreatePolicyDriver = (*Host)(nil)
	_ driver.ReusePreflightDriver = (*Host)(nil)
)

func (h *Host) SupportsMountType(kind string) bool {
	typeValue, ok := mountType(kind)
	return ok && slices.Contains(h.info.Capabilities.MountTypes, typeValue)
}

func (h *Host) RequiresMountStreaming() bool {
	return h.info.Capabilities.RequiresMountStreaming
}

func (h *Host) RequiresWorkspaceChown() bool {
	return h.info.Capabilities.RequiresWorkspaceChown
}

func (h *Host) RecreateMode() driver.RecreateMode {
	if h.info.Capabilities.RecreateMode == runtimev1.RecreateMode_RECREATE_MODE_DELETE {
		return driver.RecreateDelete
	}
	return driver.RecreateStop
}

func mountType(kind string) (runtimev1.MountType, bool) {
	switch kind {
	case driver.MountTypeBind:
		return runtimev1.MountType_MOUNT_TYPE_BIND, true
	case driver.MountTypeVolume:
		return runtimev1.MountType_MOUNT_TYPE_VOLUME, true
	case driver.MountTypeTmpfs:
		return runtimev1.MountType_MOUNT_TYPE_TMPFS, true
	default:
		return runtimev1.MountType_MOUNT_TYPE_UNSPECIFIED, false
	}
}
