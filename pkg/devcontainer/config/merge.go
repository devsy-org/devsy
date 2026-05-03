package config

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"unicode"

	"github.com/devsy-org/devsy/pkg/types"
)

const (
	gpuTrue     = "true"
	gpuFalse    = "false"
	gpuOptional = "optional"
)

func MergeExtraRemoteEnv(mergedConfig *MergedDevContainerConfig, extraConfigPath string) error {
	if extraConfigPath == "" {
		return nil
	}
	extraConfig, err := ParseDevContainerJSONFile(extraConfigPath)
	if err != nil {
		return err
	}
	maps.Copy(mergedConfig.RemoteEnv, extraConfig.RemoteEnv)
	return nil
}

func MergeConfiguration(
	config *DevContainerConfig,
	imageMetadataEntries []*ImageMetadata,
) (*MergedDevContainerConfig, error) {
	customizations := map[string][]any{}
	for _, imageMetadata := range imageMetadataEntries {
		for k, v := range imageMetadata.Customizations {
			customizations[k] = append(customizations[k], v)
		}
	}

	copiedConfig := CloneDevContainerConfig(config)

	// reverse the order
	reversed := ReverseSlice(imageMetadataEntries)

	// merge config
	mergedConfig := &MergedDevContainerConfig{
		UpdatedConfigProperties: UpdatedConfigProperties{
			Customizations: customizations,
		},
		DevContainerConfigBase: copiedConfig.DevContainerConfigBase,
		NonComposeBase:         copiedConfig.NonComposeBase,
		ImageContainer:         copiedConfig.ImageContainer,
		ComposeContainer:       copiedConfig.ComposeContainer,
		DockerfileContainer:    copiedConfig.DockerfileContainer,
	}

	// adjust config
	mergedConfig.Init = some(reversed, func(entry *ImageMetadata) *bool { return entry.Init })
	mergedConfig.Privileged = some(
		reversed,
		func(entry *ImageMetadata) *bool { return entry.Privileged },
	)
	mergedConfig.CapAdd = unique(
		unionOrNil(reversed, func(entry *ImageMetadata) []string { return entry.CapAdd }),
	)
	mergedConfig.SecurityOpt = unique(
		unionOrNil(reversed, func(entry *ImageMetadata) []string { return entry.SecurityOpt }),
	)
	mergedConfig.Entrypoints = collectOrNil(
		reversed,
		func(entry *ImageMetadata) string { return entry.Entrypoint },
	)
	mergedConfig.Mounts = mergeMounts(reversed)
	mergedConfig.OnCreateCommands = mergeLifestyleHooks(
		reversed,
		func(entry *ImageMetadata) types.LifecycleHook { return entry.OnCreateCommand },
	)
	mergedConfig.UpdateContentCommands = mergeLifestyleHooks(
		reversed,
		func(entry *ImageMetadata) types.LifecycleHook { return entry.UpdateContentCommand },
	)
	mergedConfig.PostCreateCommands = mergeLifestyleHooks(
		reversed,
		func(entry *ImageMetadata) types.LifecycleHook { return entry.PostCreateCommand },
	)
	mergedConfig.PostStartCommands = mergeLifestyleHooks(
		reversed,
		func(entry *ImageMetadata) types.LifecycleHook { return entry.PostStartCommand },
	)
	mergedConfig.PostAttachCommands = mergeLifestyleHooks(
		reversed,
		func(entry *ImageMetadata) types.LifecycleHook { return entry.PostAttachCommand },
	)
	mergedConfig.WaitFor = firstString(
		reversed,
		func(entry *ImageMetadata) string { return entry.WaitFor },
	)
	mergedConfig.RemoteUser = firstString(
		reversed,
		func(entry *ImageMetadata) string { return entry.RemoteUser },
	)
	mergedConfig.ContainerUser = firstString(
		reversed,
		func(entry *ImageMetadata) string { return entry.ContainerUser },
	)
	mergedConfig.UserEnvProbe = firstString(
		reversed,
		func(entry *ImageMetadata) string { return entry.UserEnvProbe },
	)
	mergedConfig.RemoteEnv = mergeMaps(
		reversed,
		func(entry *ImageMetadata) map[string]*string { return entry.RemoteEnv },
	)
	// Config-level remoteEnv takes precedence over image metadata remoteEnv.
	maps.Copy(mergedConfig.RemoteEnv, copiedConfig.RemoteEnv)
	mergedConfig.ContainerEnv = mergeMaps(
		reversed,
		func(entry *ImageMetadata) map[string]string { return entry.ContainerEnv },
	)
	mergedConfig.PortsAttributes = mergeMaps(
		reversed,
		func(entry *ImageMetadata) map[string]PortAttribute { return entry.PortsAttributes },
	)
	mergedConfig.OverrideCommand = some(
		reversed,
		func(entry *ImageMetadata) *bool { return entry.OverrideCommand },
	)
	mergedConfig.OtherPortsAttributes = mergeOtherPortsAttributes(reversed)
	mergedConfig.ShutdownAction = firstString(
		reversed,
		func(entry *ImageMetadata) string { return entry.ShutdownAction },
	)
	mergedConfig.ForwardPorts = mergeForwardPorts(reversed)
	mergedConfig.UpdateRemoteUserUID = some(
		reversed,
		func(entry *ImageMetadata) *bool { return entry.UpdateRemoteUserUID },
	)
	mergedConfig.HostRequirements = mergeHostRequirements(reversed)

	return mergedConfig, nil
}

func mergeOtherPortsAttributes(entries []*ImageMetadata) *PortAttribute {
	for _, entry := range entries {
		if entry.OtherPortsAttributes != nil {
			return entry.OtherPortsAttributes
		}
	}
	return nil
}

func mergeMaps[K any](
	entries []*ImageMetadata,
	m func(entry *ImageMetadata) map[string]K,
) map[string]K {
	retMap := map[string]K{}
	for _, entry := range entries {
		entryMap := m(entry)
		maps.Copy(retMap, entryMap)
	}

	return retMap
}

func firstString(entries []*ImageMetadata, m func(entry *ImageMetadata) string) string {
	for _, entry := range entries {
		str := m(entry)
		if str != "" {
			return str
		}
	}
	return ""
}

func mergeHostRequirements(entries []*ImageMetadata) *HostRequirements {
	var merged *HostRequirements
	for _, entry := range entries {
		if entry.HostRequirements == nil {
			continue
		}
		if merged == nil {
			merged = &HostRequirements{}
		}
		if entry.HostRequirements.CPUs > merged.CPUs {
			merged.CPUs = entry.HostRequirements.CPUs
		}
		merged.Memory = maxByteString(merged.Memory, entry.HostRequirements.Memory)
		merged.Storage = maxByteString(merged.Storage, entry.HostRequirements.Storage)
		merged.GPU = mergeGPU(merged.GPU, entry.HostRequirements.GPU)
	}
	return merged
}

var byteSizeMultipliers = map[string]uint64{
	"kb": 1024,
	"mb": 1024 * 1024,
	"gb": 1024 * 1024 * 1024,
	"tb": 1024 * 1024 * 1024 * 1024,
}

func parseByteSize(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	numStr := s[:i]
	unit := strings.TrimSpace(s[i:])
	unit = strings.Map(unicode.ToLower, unit)

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}
	if mult, ok := byteSizeMultipliers[unit]; ok {
		return uint64(val * float64(mult))
	}
	return uint64(val)
}

func maxByteString(a, b string) string {
	if parseByteSize(b) > parseByteSize(a) {
		return b
	}
	if a == "" {
		return b
	}
	return a
}

func gpuPriority(g *GPURequirement) int {
	if g == nil {
		return 0
	}
	switch strings.ToLower(g.Value) {
	case gpuFalse:
		return 1
	case gpuOptional:
		return 2
	default:
		return 3
	}
}

func mergeGPU(a, b *GPURequirement) *GPURequirement {
	if gpuPriority(b) > gpuPriority(a) {
		return b
	}
	return a
}

func expandPortRange(port string) ([]string, error) {
	if strings.Contains(port, ":") {
		return []string{port}, nil
	}

	startStr, endStr, hasRange := strings.Cut(port, "-")
	if !hasRange {
		if _, err := strconv.Atoi(port); err != nil {
			return nil, fmt.Errorf("invalid port %q: %w", port, err)
		}
		return []string{port}, nil
	}

	start, err := strconv.Atoi(startStr)
	if err != nil {
		return nil, fmt.Errorf("invalid range start in %q: %w", port, err)
	}
	end, err := strconv.Atoi(endStr)
	if err != nil {
		return nil, fmt.Errorf("invalid range end in %q: %w", port, err)
	}
	if start < 0 || end < 0 {
		return nil, fmt.Errorf("negative port in range %q", port)
	}
	if start > end {
		return nil, fmt.Errorf("invalid port range %q: start (%d) > end (%d)", port, start, end)
	}

	ports := make([]string, 0, end-start+1)
	for p := start; p <= end; p++ {
		ports = append(ports, strconv.Itoa(p))
	}
	return ports, nil
}

func mergeForwardPorts(entries []*ImageMetadata) types.StrIntArray {
	portMap := map[string]bool{}
	var retPorts types.StrIntArray
	for _, entry := range entries {
		for _, port := range entry.ForwardPorts {
			expanded, err := expandPortRange(port)
			if err != nil {
				continue
			}
			for _, p := range expanded {
				portString := p
				_, err := strconv.Atoi(portString)
				if err == nil {
					portString = "localhost:" + portString
				}
				if portMap[portString] {
					continue
				}

				portMap[portString] = true
				retPorts = append(retPorts, p)
			}
		}
	}

	return retPorts
}

func mergeMounts(entries []*ImageMetadata) []*Mount {
	targetMap := map[string]bool{}
	ret := []*Mount{}

	reversedEntries := ReverseSlice(entries)
	for _, entry := range reversedEntries {
		for _, mount := range entry.Mounts {
			if targetMap[mount.Target] {
				continue
			}

			ret = append(ret, mount)
			targetMap[mount.Target] = true
		}
	}
	return ReverseSlice(ret)
}

func mergeLifestyleHooks(
	entries []*ImageMetadata,
	m func(entry *ImageMetadata) types.LifecycleHook,
) []types.LifecycleHook {
	var out []types.LifecycleHook
	for _, entry := range entries {
		val := m(entry)
		if len(val) > 0 {
			out = append(out, m(entry))
		}
	}
	return out
}

func collectOrNil[T comparable, K any](entries []K, m func(entry K) T) []T {
	var out []T
	for _, entry := range entries {
		var defaultValue T
		val := m(entry)
		if val != defaultValue {
			out = append(out, m(entry))
		}
	}

	return out
}

func unionOrNil[T any, K any](entries []K, m func(entry K) []T) []T {
	var out []T
	for _, entry := range entries {
		vals := m(entry)
		if len(vals) > 0 {
			out = append(out, vals...)
		}
	}

	return out
}

func unique[T comparable](s []T) []T {
	inResult := make(map[T]bool)
	var result []T
	for _, str := range s {
		if _, ok := inResult[str]; !ok {
			inResult[str] = true
			result = append(result, str)
		}
	}
	return result
}

func some[T any](entries []T, m func(entry T) *bool) *bool {
	for _, entry := range entries {
		boolPtr := m(entry)
		if boolPtr != nil {
			return boolPtr
		}
	}
	return nil
}

func ReverseSlice[T comparable](s []T) []T {
	var r []T
	for i := len(s) - 1; i >= 0; i-- {
		r = append(r, s[i])
	}
	return r
}
