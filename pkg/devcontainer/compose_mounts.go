package devcontainer

import (
	"strconv"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

// mountToServiceVolumeConfig forwards Mount.Other options into the
// override Compose volume so they survive override generation; without
// this, bind/volume/tmpfs options are silently dropped.
func mountToServiceVolumeConfig(m *config.Mount) composetypes.ServiceVolumeConfig {
	v := composetypes.ServiceVolumeConfig{
		Type:        m.Type,
		Source:      m.Source,
		Target:      m.Target,
		ReadOnly:    m.IsReadOnly(),
		Consistency: m.Consistency(),
	}
	switch m.Type {
	case composetypes.VolumeTypeBind, "":
		v.Bind = bindOptionsFromMount(m)
	case composetypes.VolumeTypeVolume:
		v.Volume = volumeOptionsFromMount(m)
	case composetypes.VolumeTypeTmpfs:
		v.Tmpfs = tmpfsOptionsFromMount(m)
	}
	return v
}

func bindOptionsFromMount(m *config.Mount) *composetypes.ServiceVolumeBind {
	propagation := m.BindPropagation()
	nonRecursive := m.IsBindNonRecursive()
	if propagation == "" && !nonRecursive {
		return nil
	}
	bind := &composetypes.ServiceVolumeBind{Propagation: propagation}
	if nonRecursive {
		bind.Recursive = "disabled"
	}
	return bind
}

func volumeOptionsFromMount(m *config.Mount) *composetypes.ServiceVolumeVolume {
	nocopy := m.VolumeNoCopy()
	subpath := m.VolumeSubpath()
	if !nocopy && subpath == "" {
		return nil
	}
	return &composetypes.ServiceVolumeVolume{NoCopy: nocopy, Subpath: subpath}
}

func tmpfsOptionsFromMount(m *config.Mount) *composetypes.ServiceVolumeTmpfs {
	size, hasSize := parseTmpfsSize(m.TmpfsSize(), m.Target)
	mode, hasMode := parseTmpfsMode(m.TmpfsMode(), m.Target)
	if !hasSize && !hasMode {
		return nil
	}
	return &composetypes.ServiceVolumeTmpfs{Size: size, Mode: mode}
}

func parseTmpfsSize(raw, target string) (composetypes.UnitBytes, bool) {
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		log.Warnf("ignoring invalid tmpfs-size %q on mount %s", raw, target)
		return 0, false
	}
	return composetypes.UnitBytes(parsed), true
}

func parseTmpfsMode(raw, target string) (uint32, bool) {
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		log.Warnf("ignoring tmpfs-mode %q on mount %s: %s", raw, target, err)
		return 0, false
	}
	return uint32(parsed), true
}
