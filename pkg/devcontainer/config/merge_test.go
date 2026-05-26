package config

import (
	"os"
	"slices"
	"testing"

	"github.com/devsy-org/devsy/pkg/types"
)

const testPortRange = "3000-3002"

func gpu(val string) *GPURequirement {
	return &GPURequirement{Value: val}
}

func hr(h *HostRequirements) *ImageMetadata {
	return &ImageMetadata{
		DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: h,
		},
	}
}

func TestMergeHostRequirements_AllNil(t *testing.T) {
	entries := []*ImageMetadata{{}, {}, {}}
	got := mergeHostRequirements(entries)
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestMergeHostRequirements_SingleEntry(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{CPUs: 4, Memory: "8gb", Storage: "32gb", GPU: gpu(gpuTrue)}),
	}
	got := mergeHostRequirements(entries)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", got.CPUs)
	}
	if got.Memory != "8gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "8gb")
	}
	if got.Storage != "32gb" {
		t.Errorf("Storage = %q, want %q", got.Storage, "32gb")
	}
	if got.GPU == nil || got.GPU.Value != gpuTrue {
		t.Errorf("GPU = %+v, want true", got.GPU)
	}
}

func TestMergeHostRequirements_MaxCPUs(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{CPUs: 2}),
		hr(&HostRequirements{CPUs: 8}),
		hr(&HostRequirements{CPUs: 4}),
	}
	got := mergeHostRequirements(entries)
	if got.CPUs != 8 {
		t.Errorf("CPUs = %d, want 8", got.CPUs)
	}
}

func TestMergeHostRequirements_MaxMemory(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{Memory: "4gb"}),
		hr(&HostRequirements{Memory: "16gb"}),
		hr(&HostRequirements{Memory: "8gb"}),
	}
	got := mergeHostRequirements(entries)
	if got.Memory != "16gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "16gb")
	}
}

func TestMergeHostRequirements_MaxStorage(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{Storage: "64gb"}),
		hr(&HostRequirements{Storage: "1tb"}),
	}
	got := mergeHostRequirements(entries)
	if got.Storage != "1tb" {
		t.Errorf("Storage = %q, want %q", got.Storage, "1tb")
	}
}

func TestMergeHostRequirements_MixedUnits(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{Memory: "512mb"}),
		hr(&HostRequirements{Memory: "1gb"}),
	}
	got := mergeHostRequirements(entries)
	if got.Memory != "1gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "1gb")
	}
}

func TestMergeHostRequirements_GPUTrueBeatsOptional(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{GPU: gpu(gpuOptional)}),
		hr(&HostRequirements{GPU: gpu(gpuTrue)}),
	}
	got := mergeHostRequirements(entries)
	if got.GPU == nil || got.GPU.Value != gpuTrue {
		t.Errorf("GPU = %+v, want true", got.GPU)
	}
}

func TestMergeHostRequirements_GPUOptionalBeatsFalse(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{GPU: gpu(gpuFalse)}),
		hr(&HostRequirements{GPU: gpu(gpuOptional)}),
	}
	got := mergeHostRequirements(entries)
	if got.GPU == nil || got.GPU.Value != gpuOptional {
		t.Errorf("GPU = %+v, want optional", got.GPU)
	}
}

func TestMergeHostRequirements_GPUTrueBeatsFalse(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{GPU: gpu(gpuTrue)}),
		hr(&HostRequirements{GPU: gpu(gpuFalse)}),
	}
	got := mergeHostRequirements(entries)
	if got.GPU == nil || got.GPU.Value != gpuTrue {
		t.Errorf("GPU = %+v, want true", got.GPU)
	}
}

func TestMergeHostRequirements_GPUEmptyPreservesValue(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{GPU: gpu(gpuTrue)}),
		{},
	}
	got := mergeHostRequirements(entries)
	if got.GPU == nil || got.GPU.Value != gpuTrue {
		t.Errorf("GPU = %+v, want true", got.GPU)
	}
}

func TestMergeHostRequirements_MultiSource(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{CPUs: 2, Memory: "4gb"}),
		hr(&HostRequirements{CPUs: 4, Storage: "64gb", GPU: gpu(gpuOptional)}),
		hr(&HostRequirements{Memory: "8gb", GPU: gpu(gpuTrue)}),
		hr(&HostRequirements{CPUs: 1, Storage: "128gb"}),
	}
	got := mergeHostRequirements(entries)
	if got.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", got.CPUs)
	}
	if got.Memory != "8gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "8gb")
	}
	if got.Storage != "128gb" {
		t.Errorf("Storage = %q, want %q", got.Storage, "128gb")
	}
	if got.GPU == nil || got.GPU.Value != gpuTrue {
		t.Errorf("GPU = %+v, want true", got.GPU)
	}
}

func TestMergeHostRequirements_PartialFields(t *testing.T) {
	entries := []*ImageMetadata{
		hr(&HostRequirements{CPUs: 8}),
		hr(&HostRequirements{Memory: "16gb"}),
		hr(&HostRequirements{Storage: "1tb"}),
	}
	got := mergeHostRequirements(entries)
	if got.CPUs != 8 {
		t.Errorf("CPUs = %d, want 8", got.CPUs)
	}
	if got.Memory != "16gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "16gb")
	}
	if got.Storage != "1tb" {
		t.Errorf("Storage = %q, want %q", got.Storage, "1tb")
	}
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input string
		want  uint64
	}{
		{"", 0},
		{"garbage", 0},
		{"1024", 1024},
		{"4kb", 4 * 1024},
		{"512mb", 512 * 1024 * 1024},
		{"8gb", 8 * 1024 * 1024 * 1024},
		{"1tb", 1024 * 1024 * 1024 * 1024},
		{"1.5gb", uint64(1.5 * 1024 * 1024 * 1024)},
		{"  16GB  ", 16 * 1024 * 1024 * 1024},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseByteSize(tt.input)
			if got != tt.want {
				t.Errorf("parseByteSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestMaxByteString(t *testing.T) {
	tests := []struct {
		a, b string
		want string
	}{
		{"", "", ""},
		{"", "8gb", "8gb"},
		{"8gb", "", "8gb"},
		{"4gb", "8gb", "8gb"},
		{"8gb", "4gb", "8gb"},
		{"512mb", "1gb", "1gb"},
		{"1gb", "512mb", "1gb"},
		{"1tb", "512gb", "1tb"},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := maxByteString(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("maxByteString(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestExpandPortRange(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{"single port", "8080", []string{"8080"}, false},
		{"host:port passthrough", "localhost:3000", []string{"localhost:3000"}, false},
		{
			"range expands", "3000-3005",
			[]string{"3000", "3001", "3002", "3003", "3004", "3005"},
			false,
		},
		{"single element range", "8080-8080", []string{"8080"}, false},
		{"start greater than end", "3005-3000", nil, true},
		{"negative start", "-1-3000", nil, true},
		{"non-numeric start", "abc-3000", nil, true},
		{"non-numeric end", "3000-xyz", nil, true},
		{"non-numeric single port", "abc", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandPortRange(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expandPortRange(%q) expected error, got %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("expandPortRange(%q) unexpected error: %v", tt.input, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("expandPortRange(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMergeForwardPorts_RangeExpansion(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			ForwardPorts: []string{"8080", testPortRange},
		}},
	}
	got := mergeForwardPorts(entries)
	want := []string{"8080", "3000", "3001", "3002"}
	if len(got) != len(want) {
		t.Fatalf("mergeForwardPorts = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("mergeForwardPorts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMergeForwardPorts_MixedRangesAndSinglePorts(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			ForwardPorts: []string{"8080", testPortRange, "localhost:9090"},
		}},
	}
	got := mergeForwardPorts(entries)
	want := []string{"8080", "3000", "3001", "3002", "localhost:9090"}
	if len(got) != len(want) {
		t.Fatalf("mergeForwardPorts = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("mergeForwardPorts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMergeForwardPorts_DeduplicatesAcrossRanges(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			ForwardPorts: []string{testPortRange},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			ForwardPorts: []string{"3001-3003"},
		}},
	}
	got := mergeForwardPorts(entries)
	want := []string{"3000", "3001", "3002", "3003"}
	if len(got) != len(want) {
		t.Fatalf("mergeForwardPorts = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("mergeForwardPorts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMergeForwardPorts_InvalidRangeSkipped(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			ForwardPorts: []string{"8080", "5000-4000", "9090"},
		}},
	}
	got := mergeForwardPorts(entries)
	want := []string{"8080", "9090"}
	if len(got) != len(want) {
		t.Fatalf("mergeForwardPorts = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("mergeForwardPorts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMergeLifestyleHooks_FeatureBeforeImage(t *testing.T) {
	featureHook := types.LifecycleHook{"feature-cmd": {"echo feature"}}
	imageHook := types.LifecycleHook{"image-cmd": {"echo image"}}

	// Simulate reversed entries as passed to mergeLifestyleHooks:
	// [devcontainer_config_entry, feature_entry]
	entries := []*ImageMetadata{
		{DevContainerActions: DevContainerActions{OnCreateCommand: imageHook}},
		{DevContainerActions: DevContainerActions{OnCreateCommand: featureHook}},
	}

	got := mergeLifestyleHooks(entries, func(e *ImageMetadata) types.LifecycleHook {
		return e.OnCreateCommand
	})

	if len(got) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(got))
	}
	if _, ok := got[0]["feature-cmd"]; !ok {
		t.Errorf("expected feature hook first, got %v", got[0])
	}
	if _, ok := got[1]["image-cmd"]; !ok {
		t.Errorf("expected image hook second, got %v", got[1])
	}
}

func TestMergeLifestyleHooks_AllHookTypes(t *testing.T) {
	featureHook := types.LifecycleHook{"feat": {"echo feat"}}
	imageHook := types.LifecycleHook{"img": {"echo img"}}

	entries := []*ImageMetadata{
		{DevContainerActions: DevContainerActions{
			OnCreateCommand:      imageHook,
			UpdateContentCommand: imageHook,
			PostCreateCommand:    imageHook,
			PostStartCommand:     imageHook,
			PostAttachCommand:    imageHook,
		}},
		{DevContainerActions: DevContainerActions{
			OnCreateCommand:      featureHook,
			UpdateContentCommand: featureHook,
			PostCreateCommand:    featureHook,
			PostStartCommand:     featureHook,
			PostAttachCommand:    featureHook,
		}},
	}

	hookExtractors := []struct {
		name string
		fn   func(e *ImageMetadata) types.LifecycleHook
	}{
		{"onCreateCommand", func(e *ImageMetadata) types.LifecycleHook {
			return e.OnCreateCommand
		}},
		{"updateContentCommand", func(e *ImageMetadata) types.LifecycleHook {
			return e.UpdateContentCommand
		}},
		{"postCreateCommand", func(e *ImageMetadata) types.LifecycleHook {
			return e.PostCreateCommand
		}},
		{"postStartCommand", func(e *ImageMetadata) types.LifecycleHook {
			return e.PostStartCommand
		}},
		{"postAttachCommand", func(e *ImageMetadata) types.LifecycleHook {
			return e.PostAttachCommand
		}},
	}

	for _, tc := range hookExtractors {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeLifestyleHooks(entries, tc.fn)
			if len(got) != 2 {
				t.Fatalf("expected 2 hooks, got %d", len(got))
			}
			if _, ok := got[0]["feat"]; !ok {
				t.Errorf("expected feature hook first, got %v", got[0])
			}
			if _, ok := got[1]["img"]; !ok {
				t.Errorf("expected image hook second, got %v", got[1])
			}
		})
	}
}

func TestMergeLifestyleHooks_EmptyEntries(t *testing.T) {
	got := mergeLifestyleHooks(nil, func(e *ImageMetadata) types.LifecycleHook {
		return e.OnCreateCommand
	})
	if got != nil {
		t.Fatalf("expected nil for nil entries, got %v", got)
	}

	got = mergeLifestyleHooks([]*ImageMetadata{}, func(e *ImageMetadata) types.LifecycleHook {
		return e.OnCreateCommand
	})
	if got != nil {
		t.Fatalf("expected nil for empty entries, got %v", got)
	}
}

func TestMergeLifestyleHooks_SingleEntry(t *testing.T) {
	hook := types.LifecycleHook{"cmd": {"echo hello"}}
	entries := []*ImageMetadata{
		{DevContainerActions: DevContainerActions{OnCreateCommand: hook}},
	}

	got := mergeLifestyleHooks(entries, func(e *ImageMetadata) types.LifecycleHook {
		return e.OnCreateCommand
	})

	if len(got) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(got))
	}
	if _, ok := got[0]["cmd"]; !ok {
		t.Errorf("expected hook, got %v", got[0])
	}
}

func TestMergeLifestyleHooks_SkipsEmpty(t *testing.T) {
	featureHook := types.LifecycleHook{"feat": {"echo feat"}}

	entries := []*ImageMetadata{
		{},
		{DevContainerActions: DevContainerActions{OnCreateCommand: featureHook}},
		{},
	}

	got := mergeLifestyleHooks(entries, func(e *ImageMetadata) types.LifecycleHook {
		return e.OnCreateCommand
	})

	if len(got) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(got))
	}
	if _, ok := got[0]["feat"]; !ok {
		t.Errorf("expected feature hook, got %v", got[0])
	}
}

func TestMergeLifestyleHooks_MultipleFeatures(t *testing.T) {
	imageHook := types.LifecycleHook{"img": {"echo img"}}
	feature1Hook := types.LifecycleHook{"feat1": {"echo feat1"}}
	feature2Hook := types.LifecycleHook{"feat2": {"echo feat2"}}

	// After ReverseSlice in MergeConfiguration, reversed entries are:
	// [devcontainer_config, feature2 (last applied), feature1 (first applied)]
	// mergeLifestyleHooks iterates entries[1:] backward (feat1, feat2),
	// then appends entries[0] (devcontainer_config)
	entries := []*ImageMetadata{
		{DevContainerActions: DevContainerActions{OnCreateCommand: imageHook}},
		{DevContainerActions: DevContainerActions{OnCreateCommand: feature2Hook}},
		{DevContainerActions: DevContainerActions{OnCreateCommand: feature1Hook}},
	}

	got := mergeLifestyleHooks(entries, func(e *ImageMetadata) types.LifecycleHook {
		return e.OnCreateCommand
	})

	if len(got) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(got))
	}
	if _, ok := got[0]["feat1"]; !ok {
		t.Errorf("expected feature1 hook first, got %v", got[0])
	}
	if _, ok := got[1]["feat2"]; !ok {
		t.Errorf("expected feature2 hook second, got %v", got[1])
	}
	if _, ok := got[2]["img"]; !ok {
		t.Errorf("expected image hook last, got %v", got[2])
	}
}

func TestMergeConfiguration_ShutdownActionDefault_ImageConfig(t *testing.T) {
	cfg := &DevContainerConfig{
		ImageContainer: ImageContainer{Image: "ubuntu:latest"},
	}
	merged, err := MergeConfiguration(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.ShutdownAction != ShutdownActionStopContainer {
		t.Errorf("ShutdownAction = %q, want %q", merged.ShutdownAction, ShutdownActionStopContainer)
	}
}

func TestMergeConfiguration_ShutdownActionDefault_ComposeConfig(t *testing.T) {
	cfg := &DevContainerConfig{
		ComposeContainer: ComposeContainer{
			DockerComposeFile: []string{"docker-compose.yml"},
			Service:           "app",
		},
	}
	merged, err := MergeConfiguration(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.ShutdownAction != ShutdownActionStopCompose {
		t.Errorf("ShutdownAction = %q, want %q", merged.ShutdownAction, ShutdownActionStopCompose)
	}
}

// TestMergeConfiguration_NilMetadata_PropagatesLifecycleHooks asserts that
// lifecycle commands declared directly in the user's devcontainer.json are
// carried into MergedDevContainerConfig even when no image metadata entries
// are supplied. Regression test for the case where `devsy set-up` (and other
// callers passing nil metadata) silently dropped the user's postCreateCommand.
func TestMergeConfiguration_NilMetadata_PropagatesLifecycleHooks(t *testing.T) {
	postCreate := types.LifecycleHook{"": {"touch /tmp/setup-test-marker"}}
	postStart := types.LifecycleHook{"": {"echo started"}}
	onCreate := types.LifecycleHook{"": {"echo onCreate"}}

	cfg := &DevContainerConfig{
		ImageContainer: ImageContainer{Image: "alpine"},
		DevContainerActions: DevContainerActions{
			OnCreateCommand:   onCreate,
			PostCreateCommand: postCreate,
			PostStartCommand:  postStart,
		},
	}

	merged, err := MergeConfiguration(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(merged.PostCreateCommands) != 1 {
		t.Fatalf("PostCreateCommands = %v, want one entry", merged.PostCreateCommands)
	}
	if got := merged.PostCreateCommands[0][""]; len(got) != 1 ||
		got[0] != "touch /tmp/setup-test-marker" {
		t.Errorf("PostCreateCommands[0] = %v, want [touch /tmp/setup-test-marker]", got)
	}
	if len(merged.PostStartCommands) != 1 {
		t.Errorf("PostStartCommands = %v, want one entry", merged.PostStartCommands)
	}
	if len(merged.OnCreateCommands) != 1 {
		t.Errorf("OnCreateCommands = %v, want one entry", merged.OnCreateCommands)
	}
}

// TestMergeConfiguration_NilMetadata_ParsedFromJSONFile exercises the full
// parse-then-merge path used by `devsy set-up`'s loadConfig.
func TestMergeConfiguration_NilMetadata_ParsedFromJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/devcontainer.json"
	if err := os.WriteFile(
		path,
		[]byte(`{"image":"alpine","postCreateCommand":"touch /tmp/setup-test-marker"}`),
		0o600,
	); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := ParseDevContainerJSONFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	merged, err := MergeConfiguration(cfg, nil)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	if len(merged.PostCreateCommands) != 1 {
		t.Fatalf("PostCreateCommands = %v, want one entry", merged.PostCreateCommands)
	}
	cmds := merged.PostCreateCommands[0][""]
	if len(cmds) != 1 || cmds[0] != "touch /tmp/setup-test-marker" {
		t.Errorf("PostCreateCommands[0][\"\"] = %v, want [touch /tmp/setup-test-marker]", cmds)
	}
}

func TestMergeConfiguration_ShutdownActionExplicit_NotOverridden(t *testing.T) {
	cfg := &DevContainerConfig{
		DevContainerConfigBase: DevContainerConfigBase{
			ShutdownAction: ShutdownActionNone,
		},
		ComposeContainer: ComposeContainer{
			DockerComposeFile: []string{"docker-compose.yml"},
			Service:           "app",
		},
	}
	merged, err := MergeConfiguration(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.ShutdownAction != ShutdownActionNone {
		t.Errorf("ShutdownAction = %q, want %q", merged.ShutdownAction, ShutdownActionNone)
	}
}
