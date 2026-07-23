package http

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
)

func TestInsecureTLSEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset defaults to secure", value: "", want: false},
		{name: "true enables insecure", value: "true", want: true},
		{name: "1 enables insecure", value: "1", want: true},
		{name: "false stays secure", value: "false", want: false},
		{name: "garbage stays secure", value: "yes-please", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.EnvInsecureTLS, tt.value)
			if got := insecureTLSEnabled(); got != tt.want {
				t.Errorf("insecureTLSEnabled() with %q = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
