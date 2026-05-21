package config

import (
	"fmt"
	"testing"
)

func TestContextOptionSSHTunnelMode_Exists(t *testing.T) {
	found := false
	for _, opt := range ContextOptions {
		if opt.Name == ContextOptionSSHTunnelMode {
			found = true
			if opt.Default != BoolFalse {
				t.Errorf("expected default %q, got %q", BoolFalse, opt.Default)
			}
			if len(opt.Enum) != 2 || opt.Enum[0] != BoolTrue || opt.Enum[1] != BoolFalse {
				t.Errorf("expected enum [true, false], got %v", opt.Enum)
			}
			break
		}
	}
	if !found {
		t.Fatal("ContextOptionSSHTunnelMode not found in ContextOptions")
	}
}

func TestMergeContextOptions_SSHTunnelMode_SetFromEnv(t *testing.T) {
	cfg := &ContextConfig{
		Options: map[string]OptionValue{},
	}
	environ := []string{
		fmt.Sprintf("%s=%s", ContextOptionSSHTunnelMode, BoolTrue),
	}

	MergeContextOptions(cfg, environ)

	val, ok := cfg.Options[ContextOptionSSHTunnelMode]
	if !ok {
		t.Fatal("SSH_TUNNEL_MODE not merged from environment")
	}
	if val.Value != BoolTrue {
		t.Errorf("expected value %q, got %q", BoolTrue, val.Value)
	}
	if !val.UserProvided {
		t.Error("expected UserProvided to be true")
	}
}

func TestMergeContextOptions_SSHTunnelMode_NotOverridden(t *testing.T) {
	cfg := &ContextConfig{
		Options: map[string]OptionValue{
			ContextOptionSSHTunnelMode: {Value: BoolFalse, UserProvided: true},
		},
	}
	environ := []string{
		fmt.Sprintf("%s=%s", ContextOptionSSHTunnelMode, BoolTrue),
	}

	MergeContextOptions(cfg, environ)

	val := cfg.Options[ContextOptionSSHTunnelMode]
	if val.Value != BoolFalse {
		t.Errorf("expected existing value %q to be preserved, got %q", BoolFalse, val.Value)
	}
}

func TestMergeContextOptions_SSHTunnelMode_AbsentFromEnv(t *testing.T) {
	cfg := &ContextConfig{
		Options: map[string]OptionValue{},
	}

	MergeContextOptions(cfg, []string{})

	if _, ok := cfg.Options[ContextOptionSSHTunnelMode]; ok {
		t.Error("SSH_TUNNEL_MODE should not be set when absent from environment")
	}
}
