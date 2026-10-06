package external

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
)

func (h *Host) ValidateRunImageDevContainer(params *driver.RunImageDevContainerParams) error {
	request, err := runImageRequest(params)
	if err != nil {
		return err
	}
	return h.validateRunImage(request)
}

func (h *Host) RunImageDevContainer(
	ctx context.Context,
	params *driver.RunImageDevContainerParams,
) error {
	request, err := runImageRequest(params)
	if err != nil {
		return err
	}
	request.Labels = append(request.Labels, config.GetIDLabels(params.WorkspaceID, h.idLabels)...)
	if err := h.validateRunImage(request); err != nil {
		return err
	}
	if err := h.ProvisioningPreflight(ctx); err != nil {
		return err
	}
	return h.RunImage(ctx, request)
}

func runImageRequest(
	params *driver.RunImageDevContainerParams,
) (*runtimev1.RunImageRequest, error) {
	if params == nil || params.Options == nil {
		return nil, errors.New("external runtime requires image run options")
	}
	options := params.Options
	request := &runtimev1.RunImageRequest{
		WorkspaceId:       params.WorkspaceID,
		Image:             options.Image,
		ImageBuiltLocally: options.ImageBuilt,
		User:              options.User,
		Entrypoint:        options.Entrypoint,
		Args:              slices.Clone(options.Cmd),
		Environment:       maps.Clone(options.Env),
		Labels:            slices.Clone(options.Labels),
		CapAdd:            slices.Clone(options.CapAdd),
		SecurityOpt:       slices.Clone(options.SecurityOpt),
		Privileged:        copyBool(options.Privileged),
		Init:              copyBool(options.Init),
		Userns:            options.Userns,
		UidMap:            slices.Clone(options.UidMap),
		GidMap:            slices.Clone(options.GidMap),
		Platform:          options.Platform,
	}
	if err := setRunMounts(request, options); err != nil {
		return nil, err
	}
	var err error
	request.HostRequirements, err = imageHostRequirements(params.ParsedConfig)
	if err != nil {
		return nil, err
	}

	return request, nil
}

func setRunMounts(request *runtimev1.RunImageRequest, options *driver.RunOptions) error {
	var err error
	if options.WorkspaceMount != nil {
		request.WorkspaceMount, err = convertRunMount(options.WorkspaceMount)
		if err != nil {
			return err
		}
	}
	for _, mount := range options.Mounts {
		converted, err := convertRunMount(mount)
		if err != nil {
			return err
		}
		request.Mounts = append(request.Mounts, converted)
	}
	return nil
}

func imageHostRequirements(parsed *config.DevContainerConfig) (*runtimev1.HostRequirements, error) {
	if parsed == nil {
		return nil, nil
	}
	if len(parsed.RunArgs) > 0 || len(parsed.AppPort) > 0 {
		return nil, errors.New(
			"runtime protocol v1 cannot express runArgs or appPort; use resolved runtime options and port forwarding",
		)
	}
	return convertHostRequirements(parsed.HostRequirements)
}

func copyBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func convertRunMount(mount *config.Mount) (*runtimev1.Mount, error) {
	if mount == nil {
		return nil, errors.New("external runtime mount is missing")
	}
	kind, ok := mountType(mount.Type)
	if !ok {
		return nil, fmt.Errorf("external runtime does not support mount type %q", mount.Type)
	}
	if mount.External {
		return nil, errors.New("external Compose volumes are not supported by Runtime Protocol v1")
	}
	if mount.Target == "" || (kind == runtimev1.MountType_MOUNT_TYPE_BIND && mount.Source == "") {
		return nil, errors.New(
			"external runtime mounts require a target and bind mounts require a source",
		)
	}
	return &runtimev1.Mount{
		Type: kind, Source: mount.Source, Target: mount.Target,
		ReadOnly: mount.IsReadOnly(), Options: slices.Clone(mount.Other),
	}, nil
}

func convertHostRequirements(source *config.HostRequirements) (*runtimev1.HostRequirements, error) {
	if source == nil {
		return nil, nil
	}
	if source.CPUs < 0 || source.CPUs > math.MaxInt32 {
		return nil, errors.New("external runtime CPU requirement is outside the protocol range")
	}
	result := &runtimev1.HostRequirements{Cpus: int32(source.CPUs)}
	var err error
	result.MemoryBytes, err = requirementBytes(source.Memory)
	if err != nil {
		return nil, fmt.Errorf("external runtime memory requirement: %w", err)
	}
	result.StorageBytes, err = requirementBytes(source.Storage)
	if err != nil {
		return nil, fmt.Errorf("external runtime storage requirement: %w", err)
	}
	result.Gpu, err = convertGPURequirement(source.GPU)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func convertGPURequirement(source *config.GPURequirement) (*runtimev1.GpuRequirement, error) {
	if source == nil {
		return nil, nil
	}
	if source.Cores != 0 || source.GPUMemory != "" {
		return nil, errors.New("runtime protocol v1 cannot express GPU core or memory requirements")
	}
	var value runtimev1.GpuRequirementValue
	switch source.Value {
	case "false":
		value = runtimev1.GpuRequirementValue_GPU_FALSE
	case "optional":
		value = runtimev1.GpuRequirementValue_GPU_OPTIONAL
	case "true":
		value = runtimev1.GpuRequirementValue_GPU_REQUIRED
	default:
		return nil, errors.New("external runtime GPU requirement is invalid")
	}
	return &runtimev1.GpuRequirement{Value: value}, nil
}

func requirementBytes(value string) (uint64, error) {
	if value == "" {
		return 0, nil
	}
	if _, err := config.ParseSizeToBytes(value); err != nil {
		return 0, err
	}
	// Keep Dev Container size units while rejecting multiplication overflow.
	number := strings.ToLower(strings.TrimSpace(value))
	multiplier := uint64(1)
	for _, unit := range []struct {
		suffix string
		factor uint64
	}{{"kb", 1 << 10}, {"mb", 1 << 20}, {"gb", 1 << 30}, {"tb", 1 << 40}} {
		if prefix, ok := strings.CutSuffix(number, unit.suffix); ok {
			number = prefix
			multiplier = unit.factor
			break
		}
	}
	amount, err := strconv.ParseUint(strings.TrimSpace(number), 10, 64)
	if err != nil {
		return 0, errors.New("invalid byte size")
	}
	if amount > math.MaxUint64/multiplier {
		return 0, errors.New("byte size exceeds protocol range")
	}
	return amount * multiplier, nil
}
