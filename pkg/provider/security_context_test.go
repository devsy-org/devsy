package provider

import "testing"

func TestRunsFixedNonRootUser(t *testing.T) {
	cases := []struct {
		name   string
		config ProviderAgentConfig
		want   bool
	}{
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
					AgentSecurityContext: "/etc/devsy/security-context.yaml",
				},
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.config.RunsFixedNonRootUser(); got != tc.want {
				t.Errorf("RunsFixedNonRootUser() = %v, want %v", got, tc.want)
			}
		})
	}
}
