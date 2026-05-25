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
		false, ctx, match, containerEnvField, []string{testPATHKey},
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

const (
	testWorkspaceFolder                      = "/home/user/project"
	testContainerWorkspaceFolder             = "/workspaces/project"
	testDevContainerID                       = "abc123"
	testContainerWorkspaceFolderVar          = "${containerWorkspaceFolder}"
	testContainerWorkspaceFolderBasenameVar  = "${containerWorkspaceFolderBasename}"
	testLocalWorkspaceFolderVar              = "${localWorkspaceFolder}"
	testCWFKey                               = "CWF"
	testLOCKey                               = "LOC"
	testContainerWorkspaceFolderName         = "containerWorkspaceFolder"
	testContainerWorkspaceFolderBasenameName = "containerWorkspaceFolderBasename"
	testPATHKey                              = "PATH"
	testCaseResolvesCWF                      = "resolves containerWorkspaceFolder"
	testCaseResolvesCWFBasename              = "resolves containerWorkspaceFolderBasename"
	testProjectName                          = "project"
)

func TestComputeDevContainerID(t *testing.T) {
	labels := map[string]string{
		LabelLocalFolder: "/home/user/project",
		LabelConfigFile:  "/home/user/project/.devcontainer/devcontainer.json",
	}

	got := ComputeDevContainerID(labels)
	if len(got) != specDevContainerIDWidth {
		t.Errorf("ComputeDevContainerID() length = %d, want %d", len(got), specDevContainerIDWidth)
	}

	for _, c := range got {
		valid := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'v')
		if !valid {
			t.Errorf("ComputeDevContainerID() contains invalid char %q in %q", string(c), got)
			break
		}
	}
}

func TestComputeDevContainerIDDeterministic(t *testing.T) {
	labels := map[string]string{
		LabelLocalFolder: testWorkspaceFolder,
		LabelConfigFile:  testWorkspaceFolder + "/.devcontainer/devcontainer.json",
	}
	first := ComputeDevContainerID(labels)
	second := ComputeDevContainerID(labels)
	if first != second {
		t.Errorf("ComputeDevContainerID is not deterministic: %q != %q", first, second)
	}
}

func TestComputeDevContainerIDSortedKeys(t *testing.T) {
	// Keys must be sorted — order of insertion should not matter.
	labels1 := map[string]string{
		"a": "1",
		"b": "2",
	}
	labels2 := map[string]string{
		"b": "2",
		"a": "1",
	}
	if ComputeDevContainerID(labels1) != ComputeDevContainerID(labels2) {
		t.Error("ComputeDevContainerID should produce same result regardless of insertion order")
	}
}

func TestComputeDevContainerIDKnownValue(t *testing.T) {
	labels := map[string]string{
		LabelLocalFolder: "/home/user/project",
		LabelConfigFile:  "/home/user/project/.devcontainer/devcontainer.json",
	}
	got := ComputeDevContainerID(labels)
	want := "0ns9efvs2cg80a2avksvk7nqv06jrab7n2918j79h49700ucligl"
	if got != want {
		t.Errorf("ComputeDevContainerID() = %q, want %q", got, want)
	}
}

func TestDeriveDevContainerID(t *testing.T) {
	configPath := testWorkspaceFolder + "/.devcontainer/devcontainer.json"
	got := DeriveDevContainerID(testWorkspaceFolder, configPath)
	if len(got) != specDevContainerIDWidth {
		t.Errorf("DeriveDevContainerID() length = %d, want %d", len(got), specDevContainerIDWidth)
	}
}

func TestDeriveDevContainerIDDeterministic(t *testing.T) {
	configPath := testWorkspaceFolder + "/.devcontainer/devcontainer.json"
	first := DeriveDevContainerID(testWorkspaceFolder, configPath)
	second := DeriveDevContainerID(testWorkspaceFolder, configPath)
	if first != second {
		t.Errorf("DeriveDevContainerID is not deterministic: %q != %q", first, second)
	}
}

func TestLegacyDeriveDevContainerID(t *testing.T) {
	h := sha256.Sum256([]byte(testWorkspaceFolder))
	want := hex.EncodeToString(h[:])[:devContainerIDLength]

	got := LegacyDeriveDevContainerID(testWorkspaceFolder)
	if got != want {
		t.Errorf("LegacyDeriveDevContainerID(%q) = %q, want %q", testWorkspaceFolder, got, want)
	}
	if len(got) != devContainerIDLength {
		t.Errorf("expected length %d, got %d", devContainerIDLength, len(got))
	}
}

type scopeTestConfig struct {
	ContainerEnv map[string]string  `json:"containerEnv,omitempty"`
	RemoteEnv    map[string]*string `json:"remoteEnv,omitempty"`
}

func scopeTestCtx() *SubstitutionContext {
	return &SubstitutionContext{
		DevContainerID:           testDevContainerID,
		LocalWorkspaceFolder:     testWorkspaceFolder,
		ContainerWorkspaceFolder: testContainerWorkspaceFolder,
		Env:                      map[string]string{"MY_VAR": "hello"},
	}
}

func TestSubstituteContainerEnvScoping(t *testing.T) {
	ctx := scopeTestCtx()
	tests := []struct {
		name  string
		input map[string]string
		want  map[string]string
	}{
		{
			name:  testCaseResolvesCWF,
			input: map[string]string{"V": testContainerWorkspaceFolderVar},
			want:  map[string]string{"V": testContainerWorkspaceFolder},
		},
		{
			name:  testCaseResolvesCWFBasename,
			input: map[string]string{"V": testContainerWorkspaceFolderBasenameVar},
			want:  map[string]string{"V": testProjectName},
		},
		{
			name:  "preserves containerEnv references",
			input: map[string]string{"V": "${containerEnv:PATH}"},
			want:  map[string]string{"V": "${containerEnv:PATH}"},
		},
		{
			name: "resolves local-scoped variables",
			input: map[string]string{
				"LOCAL": testLocalWorkspaceFolderVar,
				"BASE":  "${localWorkspaceFolderBasename}",
				"ENV":   "${localEnv:MY_VAR}",
				"DEVID": "${devcontainerId}",
			},
			want: map[string]string{
				"LOCAL": testWorkspaceFolder,
				"BASE":  testProjectName,
				"ENV":   "hello",
				"DEVID": testDevContainerID,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out scopeTestConfig
			err := Substitute(ctx, scopeTestConfig{ContainerEnv: tt.input}, &out)
			if err != nil {
				t.Fatalf("Substitute() error: %v", err)
			}
			assertContainerEnv(t, out.ContainerEnv, tt.want)
		})
	}
}

func TestSubstituteRemoteEnvScoping(t *testing.T) {
	ctx := scopeTestCtx()
	tests := []struct {
		name  string
		input map[string]*string
		want  map[string]*string
	}{
		{
			name:  testCaseResolvesCWF,
			input: map[string]*string{"V": strPtr("/prefix${containerWorkspaceFolder}")},
			want:  map[string]*string{"V": strPtr("/prefix/workspaces/project")},
		},
		{
			name:  testCaseResolvesCWFBasename,
			input: map[string]*string{"V": strPtr(testContainerWorkspaceFolderBasenameVar)},
			want:  map[string]*string{"V": strPtr(testProjectName)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out scopeTestConfig
			err := Substitute(ctx, scopeTestConfig{RemoteEnv: tt.input}, &out)
			if err != nil {
				t.Fatalf("Substitute() error: %v", err)
			}
			assertRemoteEnv(t, out.RemoteEnv, tt.want)
		})
	}
}

func TestSubstituteMixedScoping(t *testing.T) {
	ctx := scopeTestCtx()
	input := scopeTestConfig{
		ContainerEnv: map[string]string{
			testCWFKey: testContainerWorkspaceFolderVar,
			testLOCKey: testLocalWorkspaceFolderVar,
		},
		RemoteEnv: map[string]*string{
			testCWFKey: strPtr(testContainerWorkspaceFolderVar),
			testLOCKey: strPtr(testLocalWorkspaceFolderVar),
		},
	}
	var out scopeTestConfig
	err := Substitute(ctx, input, &out)
	if err != nil {
		t.Fatalf("Substitute() error: %v", err)
	}
	assertContainerEnv(t, out.ContainerEnv, map[string]string{
		testCWFKey: testContainerWorkspaceFolder,
		testLOCKey: testWorkspaceFolder,
	})
	assertRemoteEnv(t, out.RemoteEnv, map[string]*string{
		testCWFKey: strPtr(testContainerWorkspaceFolder),
		testLOCKey: strPtr(testWorkspaceFolder),
	})
}

func TestRestrictedReplacePreservesContainerVars(t *testing.T) {
	ctx := &SubstitutionContext{
		ContainerWorkspaceFolder: testContainerWorkspaceFolder,
		LocalWorkspaceFolder:     testWorkspaceFolder,
		Env:                      map[string]string{"HOME": "/root"},
	}
	fullReplace := func(match, variable string, args []string) string {
		return replaceWithContext(false, ctx, match, variable, args)
	}
	restricted := restrictedReplace(fullReplace)

	tests := []struct {
		name     string
		match    string
		variable string
		args     []string
		want     string
	}{
		{
			name:     testCaseResolvesCWF,
			match:    testContainerWorkspaceFolderVar,
			variable: testContainerWorkspaceFolderName,
			want:     testContainerWorkspaceFolder,
		},
		{
			name:     testCaseResolvesCWFBasename,
			match:    testContainerWorkspaceFolderBasenameVar,
			variable: testContainerWorkspaceFolderBasenameName,
			want:     testProjectName,
		},
		{
			name:     "preserves containerEnv",
			match:    "${containerEnv:PATH}",
			variable: containerEnvField,
			args:     []string{testPATHKey},
			want:     "${containerEnv:PATH}",
		},
		{
			name:     "resolves localWorkspaceFolder",
			match:    testLocalWorkspaceFolderVar,
			variable: "localWorkspaceFolder",
			want:     testWorkspaceFolder,
		},
		{
			name:     "resolves localEnv",
			match:    "${localEnv:HOME}",
			variable: "localEnv",
			args:     []string{"HOME"},
			want:     "/root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := restricted(tt.match, tt.variable, tt.args)
			if got != tt.want {
				t.Errorf("restrictedReplace() = %q, want %q", got, tt.want)
			}
		})
	}
}

func assertContainerEnv(t *testing.T, got, want map[string]string) {
	t.Helper()
	if want == nil {
		return
	}
	for k, wantVal := range want {
		gotVal, ok := got[k]
		if !ok {
			t.Errorf("containerEnv[%q] missing", k)
		} else if gotVal != wantVal {
			t.Errorf("containerEnv[%q] = %q, want %q", k, gotVal, wantVal)
		}
	}
}

func assertRemoteEnv(t *testing.T, got, want map[string]*string) {
	t.Helper()
	if want == nil {
		return
	}
	for k, wantVal := range want {
		gotVal, ok := got[k]
		switch {
		case !ok:
			t.Errorf("remoteEnv[%q] missing", k)
		case gotVal == nil:
			t.Errorf("remoteEnv[%q] = nil, want %q", k, *wantVal)
		case *gotVal != *wantVal:
			t.Errorf("remoteEnv[%q] = %q, want %q", k, *gotVal, *wantVal)
		}
	}
}
