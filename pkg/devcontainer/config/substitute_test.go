package config

import "testing"

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

func TestReplaceWithContextUnknownVarReturnsEmpty(t *testing.T) {
	ctx := &SubstitutionContext{
		Env: map[string]string{"HOME": "/root"},
	}
	result := replaceWithContext(
		false, ctx, "${unknownVar}", "unknownVar", nil,
	)
	if result != "" {
		t.Errorf(
			"expected empty string for unknown var, got %q",
			result,
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

func TestReplaceWithContainerEnvUnknownReturnsEmpty(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin"}
	result := replaceWithContainerEnv(
		env, "${unknownVar}", "unknownVar", nil,
	)
	if result != "" {
		t.Errorf(
			"expected empty string for unknown var, got %q",
			result,
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
