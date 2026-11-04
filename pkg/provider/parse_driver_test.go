package provider

import (
	"strings"
	"testing"
)

func TestValidateAgentDriver_Templated(t *testing.T) {
	base := `name: aws
version: v1.0.0
agent:
  path: x
  driver: %s
exec:
  command: echo hi
  create: echo hi
  delete: echo hi
`

	cases := []struct {
		name    string
		driver  string
		wantErr bool
	}{
		{name: "empty", driver: "", wantErr: false},
		{name: "docker literal", driver: DockerDriver, wantErr: false},
		{name: "kubernetes literal", driver: KubernetesDriver, wantErr: false},
		{name: "templated braces", driver: "${AWS_DEPLOYMENT_MODE}", wantErr: false},
		{name: "templated bare", driver: "$AWS_DEPLOYMENT_MODE", wantErr: false},
		{name: "invalid literal", driver: podmanProviderName, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProvider(strings.NewReader(strings.Replace(base, "%s", tc.driver, 1)))
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for driver %q, got nil", tc.driver)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for driver %q: %v", tc.driver, err)
			}
		})
	}
}

func TestProviderCancellationMode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "default", valid: true},
		{name: string(ProviderCancellationFile), value: string(ProviderCancellationFile), valid: true},
		{name: "unknown", value: "signal"},
		{name: templatePolicyCase, value: "${CANCELLATION}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := "name: example\nversion: v1.0.0\nexec:\n  command: echo ok\n"
			if tc.value != "" {
				manifest += "  cancellation: '" + tc.value + "'\n"
			}
			parsed, err := ParseProvider(strings.NewReader(manifest))
			if !tc.valid {
				if err == nil {
					t.Fatal("expected invalid cancellation mode")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Exec.Cancellation != ProviderCancellationMode(tc.value) {
				t.Fatalf("unexpected cancellation mode: %q", parsed.Exec.Cancellation)
			}
		})
	}
}
