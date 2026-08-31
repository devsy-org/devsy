package provider

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
)

type runsFixedNonRootUserCase struct {
	name   string
	config ProviderAgentConfig
	want   bool
}

var basicRunsFixedNonRootUserCases = []runsFixedNonRootUserCase{
	{
		name: "non-kubernetes driver ignores security context",
		config: ProviderAgentConfig{
			Driver: DockerDriver,
			Kubernetes: ProviderKubernetesDriverConfig{
				AgentSecurityContext: "runAsNonRoot: true",
			},
		},
		want: false,
	},
	{
		name: "strict security alone is not a guarantee",
		config: ProviderAgentConfig{
			Driver:     KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{StrictSecurity: "true"},
		},
		want: false,
	},
	{
		name: "capabilities-only security context is not a guarantee",
		config: ProviderAgentConfig{
			Driver: KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{
				AgentSecurityContext: "capabilities:\n  add: [\"SYS_PTRACE\"]",
			},
		},
		want: false,
	},
	{
		name: "explicit runAsNonRoot true is a guarantee",
		config: ProviderAgentConfig{
			Driver: KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{
				AgentSecurityContext: "runAsNonRoot: true",
			},
		},
		want: true,
	},
	{
		name: "explicit nonzero runAsUser is a guarantee",
		config: ProviderAgentConfig{
			Driver:     KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{AgentSecurityContext: "runAsUser: 1000"},
		},
		want: true,
	},
	{
		name: "runAsUser zero is not a guarantee",
		config: ProviderAgentConfig{
			Driver:     KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{AgentSecurityContext: "runAsUser: 0"},
		},
		want: false,
	},
	{
		name: "unparseable security context is not a guarantee",
		config: ProviderAgentConfig{
			Driver: KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{
				AgentSecurityContext: "runAsUser: [",
			},
		},
		want: false,
	},
}

func TestRunsFixedNonRootUser(t *testing.T) {
	for _, tc := range basicRunsFixedNonRootUserCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.config.RunsFixedNonRootUser(); got != tc.want {
				t.Errorf("RunsFixedNonRootUser() = %v, want %v", got, tc.want)
			}
		})
	}
}

var podManifestTemplateRunsFixedNonRootUserCases = []runsFixedNonRootUserCase{
	{
		name: "podManifestTemplate devsy container overrides agentSecurityContext to root",
		config: ProviderAgentConfig{
			Driver: KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{
				AgentSecurityContext: "runAsUser: 1000\nrunAsNonRoot: true",
				PodManifestTemplate: "spec:\n  containers:\n" +
					"  - name: " + config.BinaryName + "\n" +
					"    securityContext:\n      runAsUser: 0\n      runAsNonRoot: false\n",
			},
		},
		want: false,
	},
	{
		name: "podManifestTemplate devsy container alone guarantees non-root",
		config: ProviderAgentConfig{
			Driver: KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{
				PodManifestTemplate: "spec:\n  containers:\n" +
					"  - name: " + config.BinaryName + "\n" +
					"    securityContext:\n      runAsUser: 5000\n",
			},
		},
		want: true,
	},
	{
		name: "podManifestTemplate for a different container is ignored",
		config: ProviderAgentConfig{
			Driver: KubernetesDriver,
			Kubernetes: ProviderKubernetesDriverConfig{
				AgentSecurityContext: "runAsUser: 1000",
				PodManifestTemplate: "spec:\n  containers:\n" +
					"  - name: sidecar\n" +
					"    securityContext:\n      runAsUser: 0\n",
			},
		},
		want: true,
	},
}

func TestRunsFixedNonRootUser_PodManifestTemplatePrecedence(t *testing.T) {
	for _, tc := range podManifestTemplateRunsFixedNonRootUserCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.config.RunsFixedNonRootUser(); got != tc.want {
				t.Errorf("RunsFixedNonRootUser() = %v, want %v", got, tc.want)
			}
		})
	}
}
