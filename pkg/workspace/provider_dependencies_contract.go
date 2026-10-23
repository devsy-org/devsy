package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/providers"
)

const (
	providerDependencyPodman     = "podman"
	providerDependencyApple      = "apple"
	providerDependencyKubernetes = "kubernetes"
)

type providerDependencyBinding struct {
	execution     string
	tool          string
	option        string
	command       string
	configuration bool
}

func providerDependencyContract(
	catalog *dependencies.Catalog,
	manifest provider.ProviderConfig,
) (dependencies.ProviderContract, error) {
	// A fixed, unmatched identity prevents unreviewed provenance or command strings entering survey diagnostics.
	unsupported := dependencies.ProviderContract{
		Source: dependencies.ProviderSource{
			Kind:     dependencies.SourceFile,
			Location: "unreviewed-provider",
		},
	}
	source := manifest.Source
	if !providerDependencyInternalSource(source) {
		return unsupported, nil
	}
	binding, recognized := providerDependencyBuiltIn(source.Raw)
	if !recognized {
		return unsupported, nil
	}
	verified, err := binding.matchesEmbedded(manifest)
	if err != nil {
		return dependencies.ProviderContract{}, err
	}
	if !verified {
		return unsupported, nil
	}
	contract := dependencies.ProviderContract{
		Source: dependencies.ProviderSource{
			Kind:       dependencies.SourceInternal,
			InternalID: source.Raw,
		},
		ExecutionContract: binding.execution,
	}
	match, err := catalog.Match(contract)
	if err != nil || !binding.matchesCatalog(match) {
		return unsupported, nil
	}
	return contract, nil
}

func providerDependencyInternalSource(source provider.ProviderSource) bool {
	return source.Internal && source.Github == "" && source.File == "" && source.URL == ""
}

func providerDependencyBuiltIn(source string) (providerDependencyBinding, bool) {
	switch source {
	case DefaultDockerCommand:
		return providerDependencyBinding{
			execution: "internal_docker", tool: DefaultDockerCommand,
			option: "DOCKER_PATH", command: DefaultDockerCommand,
		}, true
	case providerDependencyPodman:
		return providerDependencyBinding{
			execution: "internal_podman", tool: providerDependencyPodman,
			option: "PODMAN_PATH", command: providerDependencyPodman,
		}, true
	case providerDependencyApple:
		return providerDependencyBinding{
			execution: "internal_apple", tool: "apple-container",
			option: "CONTAINER_PATH", command: defaultContainerCommand,
		}, true
	case providerDependencyKubernetes:
		return providerDependencyBinding{
			execution: "internal_kubernetes", tool: "kubernetes-config",
			option: "KUBERNETES_CONFIG", configuration: true,
		}, true
	default:
		return providerDependencyBinding{}, false
	}
}

func (b providerDependencyBinding) matchesEmbedded(manifest provider.ProviderConfig) (bool, error) {
	embedded, err := provider.ParseProvider(
		strings.NewReader(providers.GetBuiltInProviders()[manifest.Source.Raw]),
	)
	if err != nil {
		return false, errors.New("invalid embedded provider dependency contract")
	}
	equal, err := sameProviderDependencyManifest(manifest, *embedded)
	return equal && b.matchesManifest(*embedded), err
}

func sameProviderDependencyManifest(left, right provider.ProviderConfig) (bool, error) {
	// Compare the full typed, unexpanded manifest. Only installation name and provenance may differ.
	left.Name, right.Name = "", ""
	left.Source, right.Source = provider.ProviderSource{}, provider.ProviderSource{}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false, errors.New("cannot compare provider dependency contract")
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func (b providerDependencyBinding) matchesManifest(manifest provider.ProviderConfig) bool {
	return b.matchesOption(manifest.Options[b.option]) && manifest.Agent.Local == "true" &&
		b.matchesAgent(manifest.Agent)
}

func (b providerDependencyBinding) matchesOption(option *types.Option) bool {
	return option != nil && option.Default == b.command && option.Command == "" &&
		option.SubOptionsCommand == ""
}

func (b providerDependencyBinding) matchesAgent(agent provider.ProviderAgentConfig) bool {
	path := "${" + b.option + "}"
	switch b.tool {
	case DefaultDockerCommand, providerDependencyPodman:
		return b.matchesDockerAgent(agent)
	case "apple-container":
		return agent.Driver == provider.AppleDriver && agent.Apple.Path == path
	case "kubernetes-config":
		return agent.Driver == provider.KubernetesDriver &&
			agent.Kubernetes.KubernetesConfig == path
	default:
		return false
	}
}

func (b providerDependencyBinding) matchesDockerAgent(agent provider.ProviderAgentConfig) bool {
	return agent.IsDockerDriver() && agent.Docker.Path == "${"+b.option+"}" &&
		agent.Docker.Runtime == providerDependencyDockerRuntime(b.tool)
}

func providerDependencyDockerRuntime(tool string) string {
	if tool == providerDependencyPodman {
		return tool
	}
	return ""
}

func (b providerDependencyBinding) matchesCatalog(match dependencies.ContractMatch) bool {
	if !match.ContractVerified || len(match.Requirements) != 1 {
		return false
	}
	requirement := match.Requirements[0]
	if requirement.ToolID != b.tool || requirement.Scope != "controller_host" {
		return false
	}
	if b.configuration {
		return requirement.ConfigurationOption == b.option && requirement.ExecutableOption == ""
	}
	return requirement.ExecutableOption == b.option && requirement.ConfigurationOption == ""
}
