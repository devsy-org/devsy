package dependencies

import (
	"errors"
	"fmt"
	"slices"
)

func validateBinding(binding Binding) error {
	source, execution := bindingIdentity(binding.ID)
	if execution == "" || source != binding.Source || execution != binding.ExecutionContract {
		return errors.New("unrecognized source or execution contract")
	}
	if binding.AdapterSemverRange != "" {
		return errors.New("no managed adapter range has been released and reviewed")
	}
	expectedSchema := 0
	if binding.ID == toolMicrosandbox {
		expectedSchema = 1
	}
	if binding.RequiredRuntimeCheckSchema != expectedSchema {
		return errors.New("unrecognized runtime-check schema requirement")
	}
	if err := validateBindingPlatforms(binding); err != nil {
		return err
	}
	return validateRequirements(binding)
}

func bindingIdentity(id string) (ProviderSource, string) {
	switch id {
	case toolDocker, "podman", toolApple, "kubernetes":
		return ProviderSource{Kind: SourceInternal, InternalID: id}, "internal_" + id
	case toolMicrosandbox:
		return externalSource(id), executionMicrosandbox
	case toolLima, toolOrbStack:
		return externalSource(id), id + "_exec_v1"
	default:
		return ProviderSource{}, ""
	}
}

func validateBindingPlatforms(binding Binding) error {
	expected := []string{
		platformLinuxAMD64,
		"linux/arm64",
		"darwin/amd64",
		platformDarwinARM64,
		"windows/amd64",
		"windows/arm64",
	}
	switch binding.ID {
	case toolApple:
		expected = []string{platformDarwinARM64}
	case toolMicrosandbox:
		expected = []string{
			platformLinuxAMD64, "linux/arm64", platformDarwinARM64, "windows/amd64",
		}
	case toolLima:
		expected = expected[:4]
	case toolOrbStack:
		expected = []string{"darwin/amd64", platformDarwinARM64}
	}
	if !slices.Equal(binding.Platforms, expected) {
		return errors.New("unsupported or duplicate binding platforms")
	}
	return nil
}

func validateRequirements(binding Binding) error {
	expected := []string{bindingRequirementKey(binding.ID)}
	if binding.ID == toolMicrosandbox {
		expected = append(expected, "docker:image_backend")
	}
	if len(binding.Requirements) != len(expected) {
		return errors.New("missing or unexpected requirement")
	}
	for i, requirement := range binding.Requirements {
		key := requirement.ToolID + ":" + requirement.Purpose
		if key != expected[i] {
			return errors.New("duplicate or unexpected requirement tool")
		}
		if err := validateRequirement(requirement); err != nil {
			return err
		}
	}
	if binding.GuidanceID != binding.Requirements[0].GuidanceID {
		return errors.New("unrecognized binding guidance")
	}
	return nil
}

func bindingRequirementKey(bindingID string) string {
	switch bindingID {
	case toolApple:
		return "apple-container:container_runtime"
	case "kubernetes":
		return "kubernetes-config:cluster_configuration"
	case toolMicrosandbox:
		return "microsandbox:microvm_runtime"
	case toolLima, toolOrbStack:
		return bindingID + ":vm_runtime"
	default:
		return bindingID + ":container_runtime"
	}
}

type requirementContract struct {
	option, configuration, probe, guidance, condition string
}

func validateRequirement(requirement Requirement) error {
	expected, ok := requirementIdentity(requirement.ToolID + ":" + requirement.Purpose)
	actual := requirementContract{
		requirement.ExecutableOption, requirement.ConfigurationOption,
		requirement.ProbeID, requirement.GuidanceID, requirement.WhenImageBackend,
	}
	if !ok || actual != expected || requirement.Scope != "controller_host" {
		return errors.New("unrecognized requirement purpose, scope, option, probe or guidance")
	}
	if len(requirement.RequiredFor) == 0 {
		return errors.New("requirement operations are required")
	}
	seen := make(map[string]bool)
	for _, operation := range requirement.RequiredFor {
		if !slices.Contains(
			[]string{"configure", "create", "exec", "stop", "delete", "image-build"},
			operation,
		) ||
			seen[operation] {
			return fmt.Errorf("unrecognized or duplicate operation %q", operation)
		}
		seen[operation] = true
	}
	return nil
}

func requirementIdentity(key string) (requirementContract, bool) {
	contracts := map[string]requirementContract{
		"docker:container_runtime": {
			option:   "DOCKER_PATH",
			probe:    "docker_version",
			guidance: "docker_setup",
		},
		"docker:image_backend": {
			probe:     "docker_version",
			guidance:  "docker_setup",
			condition: toolDocker,
		},
		"podman:container_runtime": {
			option:   "PODMAN_PATH",
			probe:    "podman_version",
			guidance: "podman_setup",
		},
		"apple-container:container_runtime": {
			option:   "CONTAINER_PATH",
			probe:    "container_version",
			guidance: "apple_container_setup",
		},
		"kubernetes-config:cluster_configuration": {
			configuration: "KUBERNETES_CONFIG",
			guidance:      "kubernetes_configuration",
		},
		"microsandbox:microvm_runtime": {
			probe:    "microsandbox_version",
			guidance: "microsandbox_setup",
		},
		"lima:vm_runtime": {
			option:   "LIMA_PATH",
			probe:    "limactl_version",
			guidance: "lima_setup",
		},
		"orbstack:vm_runtime": {
			option:   "ORBSTACK_PATH",
			probe:    "orbctl_version",
			guidance: "orbstack_setup",
		},
	}
	contract, ok := contracts[key]
	return contract, ok
}
