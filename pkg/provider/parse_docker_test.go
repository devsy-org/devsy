package provider

import (
	"strings"
	"testing"
)

const (
	enabledDockerPolicy  = "true"
	disabledDockerPolicy = "false"
)

func TestParseDockerConfigureDaemon(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "unset"},
		{name: "enabled", value: enabledDockerPolicy},
		{name: "disabled", value: disabledDockerPolicy},
		{name: "template", value: "${CONFIGURE_DAEMON}"},
		{name: "bare template", value: "$CONFIGURE_DAEMON"},
		{name: "invalid", value: "sometimes", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := "name: docker-policy\nagent:\n  driver: docker\n"
			if tc.value != "" {
				manifest += "  docker:\n    configureDaemon: " + tc.value + "\n"
			}
			manifest += "exec:\n  command: echo hi\n"
			parsed, err := ParseProvider(strings.NewReader(manifest))
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "agent.docker.configureDaemon") {
					t.Fatalf("ParseProvider() error = %v, want Docker daemon policy error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(parsed.Agent.Docker.ConfigureDaemon) != tc.value {
				t.Errorf(
					"ConfigureDaemon = %q, want %q",
					parsed.Agent.Docker.ConfigureDaemon,
					tc.value,
				)
			}
		})
	}
}
