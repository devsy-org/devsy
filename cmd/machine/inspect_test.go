package machine

import (
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

func TestMaskMachineOptions_RedactsPasswordAndRemovesHidden(t *testing.T) {
	machineConfig := &provider.Machine{
		Provider: provider.MachineProviderConfig{
			Options: map[string]pkgconfig.OptionValue{
				"PLAIN":   {Value: "visible"},
				"SECRET":  {Value: "super-secret"},
				"HIDDEN":  {Value: "should-not-appear"},
				"ABSENT":  {Value: "no-matching-provider-option"},
			},
		},
	}
	p := &provider.ProviderConfig{
		Options: map[string]*types.Option{
			"PLAIN":  {},
			"SECRET": {Password: true},
			"HIDDEN": {Hidden: true},
		},
	}

	maskMachineOptions(machineConfig, p)

	if v := machineConfig.Provider.Options["PLAIN"].Value; v != "visible" {
		t.Errorf("non-password option value changed: got %q, want %q", v, "visible")
	}
	if v := machineConfig.Provider.Options["SECRET"].Value; v != "********" {
		t.Errorf("password option value not masked: got %q, want %q", v, "********")
	}
	if _, ok := machineConfig.Provider.Options["HIDDEN"]; ok {
		t.Error("hidden provider option was not removed from machine config")
	}
	if v := machineConfig.Provider.Options["ABSENT"].Value; v != "no-matching-provider-option" {
		t.Errorf("option with no matching provider entry changed: got %q", v)
	}
}
