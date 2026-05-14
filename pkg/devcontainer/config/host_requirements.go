package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/log"
)

// HostInfo provides system resource information for validation.
// Abstracted as an interface to allow testing with mock values.
type HostInfo interface {
	NumCPU() int
	TotalMemoryBytes() (uint64, error)
	AvailableStorageBytes(path string) (uint64, error)
	GPUAvailable() bool
}

// ErrHostRequirementsNotMet is returned when the host does not satisfy hard
// requirements (CPU count, memory, or GPU) declared in devcontainer.json.
var ErrHostRequirementsNotMet = errors.New("host does not meet minimum requirements")

// ValidateHostRequirements checks whether the host satisfies the resource
// requirements declared in devcontainer.json. Hard failures (insufficient CPUs,
// memory, or required GPU unavailable) are collected into a non-nil error.
// Soft issues (detection errors, insufficient storage) are returned as warnings.
func ValidateHostRequirements(
	reqs *HostRequirements, host HostInfo, workspacePath string,
) (warnings []string, err error) {
	if reqs == nil {
		return nil, nil
	}

	var hardFailures []string
	hardFailures, warnings = appendCPUCheck(hardFailures, warnings, reqs.CPUs, host)
	hardFailures, warnings = appendMemoryCheck(hardFailures, warnings, reqs.Memory, host)
	warnings = appendStorageWarning(warnings, reqs.Storage, host, workspacePath)
	hardFailures = appendGPUCheck(hardFailures, reqs.GPU, host)

	for _, w := range warnings {
		log.Warnw("hostRequirements not met", "warning", w)
	}
	for _, f := range hardFailures {
		log.Warnw("hostRequirements not met", "error", f)
	}

	if len(hardFailures) > 0 {
		return warnings, fmt.Errorf(
			"%w: %s", ErrHostRequirementsNotMet, strings.Join(hardFailures, "; "),
		)
	}
	return warnings, nil
}

func appendCPUCheck(failures, warnings []string, required int, host HostInfo) ([]string, []string) {
	if required <= 0 {
		return failures, warnings
	}
	available := host.NumCPU()
	if available < required {
		failures = append(failures, fmt.Sprintf(
			"cpus: required %d, available %d", required, available,
		))
	}
	return failures, warnings
}

func appendMemoryCheck(
	failures, warnings []string, required string, host HostInfo,
) ([]string, []string) {
	if required == "" {
		return failures, warnings
	}
	reqBytes, err := ParseSizeToBytes(required)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("memory: invalid value %q: %v", required, err))
		return failures, warnings
	}
	available, err := host.TotalMemoryBytes()
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("memory: unable to detect: %v", err))
		return failures, warnings
	}
	if available < reqBytes {
		failures = append(failures, fmt.Sprintf(
			"memory: required %s (%d bytes), available %d bytes",
			required, reqBytes, available,
		))
	}
	return failures, warnings
}

func appendGPUCheck(failures []string, gpu *GPURequirement, host HostInfo) []string {
	if gpu == nil {
		return failures
	}
	if gpu.Value == gpuOptional || gpu.Value == gpuFalse {
		return failures
	}
	required, err := strconv.ParseBool(gpu.Value)
	if err != nil || !required {
		return failures
	}
	if !host.GPUAvailable() {
		return append(failures, "gpu: required but not available on host")
	}
	return failures
}

func appendStorageWarning(
	warnings []string, required string, host HostInfo, path string,
) []string {
	if required == "" {
		return warnings
	}
	reqBytes, err := ParseSizeToBytes(required)
	if err != nil {
		return append(warnings, fmt.Sprintf("storage: invalid value %q: %v", required, err))
	}
	available, err := host.AvailableStorageBytes(path)
	if err != nil {
		return append(warnings, fmt.Sprintf("storage: unable to detect at %q: %v", path, err))
	}
	if available < reqBytes {
		return append(warnings, fmt.Sprintf(
			"storage: required %s (%d bytes), available %d bytes at %q",
			required, reqBytes, available, path,
		))
	}
	return warnings
}

var sizePattern = regexp.MustCompile(`(?i)^\s*(\d+)\s*(tb|gb|mb|kb)?\s*$`)

// ParseSizeToBytes converts a human-readable size string (e.g. "8gb", "512mb")
// to bytes. If no unit suffix is present, the value is treated as bytes.
func ParseSizeToBytes(s string) (uint64, error) {
	matches := sizePattern.FindStringSubmatch(s)
	if matches == nil {
		return 0, fmt.Errorf("unrecognized size format: %q", s)
	}
	val, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return 0, err
	}
	unit := strings.ToLower(matches[2])
	switch unit {
	case "tb":
		return val * 1024 * 1024 * 1024 * 1024, nil
	case "gb":
		return val * 1024 * 1024 * 1024, nil
	case "mb":
		return val * 1024 * 1024, nil
	case "kb":
		return val * 1024, nil
	default:
		return val, nil
	}
}
