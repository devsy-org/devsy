package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsPlatformRunner(t *testing.T) {
	tests := []struct {
		name        string
		newValue    string
		legacyValue string
		want        bool
	}{
		{name: "neither set", want: false},
		{name: "only new set to true", newValue: BoolTrue, want: true},
		{name: "only legacy set to true", legacyValue: BoolTrue, want: true},
		{name: "both set to true", newValue: BoolTrue, legacyValue: BoolTrue, want: true},
		{
			name:        "new false wins over legacy true",
			newValue:    BoolFalse,
			legacyValue: BoolTrue,
			want:        false,
		},
		{
			name:        "new true wins over legacy false",
			newValue:    BoolTrue,
			legacyValue: BoolFalse,
			want:        true,
		},
		{name: "only legacy set to false", legacyValue: BoolFalse, want: false},
		{name: "empty new falls back to legacy", newValue: "", legacyValue: BoolTrue, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvPlatformRunner, tt.newValue)
			t.Setenv(EnvPlatformRunnerLegacy, tt.legacyValue)
			assert.Equal(t, tt.want, IsPlatformRunner())
		})
	}
}
