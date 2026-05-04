package config

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestLookupValue(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{
			name: "var set no default",
			env:  map[string]string{"HOME": "/root"},
			args: []string{"HOME"},
			want: "/root",
		},
		{
			name: "var unset no default",
			env:  map[string]string{},
			args: []string{"MISSING"},
			want: "",
		},
		{
			name: "var unset simple default",
			env:  map[string]string{},
			args: []string{"MISSING", "fallback"},
			want: "fallback",
		},
		{
			name: "var unset default with colons",
			env:  map[string]string{},
			args: []string{
				"MISSING", "http",
				"//proxy.example.com", "8080",
			},
			want: "http://proxy.example.com:8080",
		},
		{
			name: "var set default ignored",
			env:  map[string]string{"VAR": "real"},
			args: []string{"VAR", "default"},
			want: "real",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lookupValue(
				false, tt.env, tt.args, "${match}",
			)
			if got != tt.want {
				t.Errorf(
					"lookupValue() = %q, want %q",
					got, tt.want,
				)
			}
		})
	}
}

func TestReplaceWithContextUnknownVarPreservedAsLiteral(t *testing.T) {
	ctx := &SubstitutionContext{
		Env: map[string]string{"HOME": "/root"},
	}
	match := "${unknownVar}"
	result := replaceWithContext(
		false, ctx, match, "unknownVar", nil,
	)
	if result != match {
		t.Errorf(
			"expected unknown var preserved as %q, got %q",
			match, result,
		)
	}
}

func TestReplaceWithContextPreservesContainerEnv(t *testing.T) {
	ctx := &SubstitutionContext{}
	match := "${containerEnv:PATH}"
	result := replaceWithContext(
		false, ctx, match, "containerEnv", []string{"PATH"},
	)
	if result != match {
		t.Errorf(
			"expected containerEnv preserved as %q, got %q",
			match, result,
		)
	}
}

func TestReplaceWithContainerEnvUnknownPreservedAsLiteral(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin"}
	match := "${unknownVar}"
	result := replaceWithContainerEnv(
		env, match, "unknownVar", nil,
	)
	if result != match {
		t.Errorf(
			"expected unknown var preserved as %q, got %q",
			match, result,
		)
	}
}

func TestReplaceWithContextLocalEnvResolves(t *testing.T) {
	ctx := &SubstitutionContext{
		Env: map[string]string{"HOME": "/root"},
	}
	match := "${localEnv:HOME}"
	result := replaceWithContext(
		false, ctx, match, "localEnv", []string{"HOME"},
	)
	if result != "/root" {
		t.Errorf(
			"expected localEnv to resolve to %q, got %q",
			"/root", result,
		)
	}
}

func TestReplaceWithContextEnvPreservedAsLiteral(t *testing.T) {
	ctx := &SubstitutionContext{
		Env: map[string]string{"HOME": "/root"},
	}
	match := "${env:HOME}"
	result := replaceWithContext(
		false, ctx, match, "env", []string{"HOME"},
	)
	if result != match {
		t.Errorf(
			"expected env var preserved as %q, got %q",
			match, result,
		)
	}
}

func TestReplaceWithContextEnvDefaultPreservedAsLiteral(t *testing.T) {
	ctx := &SubstitutionContext{
		Env: map[string]string{},
	}
	match := "${env:MISSING:default}"
	result := replaceWithContext(
		false, ctx, match, "env", []string{"MISSING", "default"},
	)
	if result != match {
		t.Errorf(
			"expected env with default preserved as %q, got %q",
			match, result,
		)
	}
}

func TestResolveStringDefaultWithColons(t *testing.T) {
	replace := func(_, variable string, args []string) string {
		env := map[string]string{}
		return lookupValue(false, env, args, "${"+variable+"}")
	}

	got := ResolveString(
		"${localEnv:MISSING:http://x:8080}", replace,
	)
	want := "http://x:8080"
	if got != want {
		t.Errorf(
			"ResolveString() = %q, want %q", got, want,
		)
	}
}

const testWorkspaceFolder = "/home/user/project"

func TestDeriveDevContainerID(t *testing.T) {
	h := sha256.Sum256([]byte(testWorkspaceFolder))
	want := hex.EncodeToString(h[:])[:devContainerIDLength]

	got := DeriveDevContainerID(testWorkspaceFolder)
	if got != want {
		t.Errorf("DeriveDevContainerID(%q) = %q, want %q", testWorkspaceFolder, got, want)
	}
	if len(got) != devContainerIDLength {
		t.Errorf("expected length %d, got %d", devContainerIDLength, len(got))
	}
}

func TestDeriveDevContainerIDDeterministic(t *testing.T) {
	first := DeriveDevContainerID(testWorkspaceFolder)
	second := DeriveDevContainerID(testWorkspaceFolder)
	if first != second {
		t.Errorf("DeriveDevContainerID is not deterministic: %q != %q", first, second)
	}
}

func TestGetLegacyDevContainerID(t *testing.T) {
	labels := map[string]string{
		"dev.containers.id": "test-workspace",
	}
	got := GetLegacyDevContainerID(labels)
	if got == "" {
		t.Error("GetLegacyDevContainerID returned empty string")
	}

	again := GetLegacyDevContainerID(labels)
	if got != again {
		t.Errorf("GetLegacyDevContainerID is not deterministic: %q != %q", got, again)
	}
}

func TestResolveDevContainerID(t *testing.T) {
	labels := map[string]string{
		"dev.containers.id": "test-workspace",
	}

	got := ResolveDevContainerID(testWorkspaceFolder, labels)
	want := DeriveDevContainerID(testWorkspaceFolder)
	if got != want {
		t.Errorf("ResolveDevContainerID() = %q, want %q (spec-based ID)", got, want)
	}
}
