package config

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/types"
)

const gpuTrue types.StrBool = "true"

func TestMergeHostRequirements_AllNil(t *testing.T) {
	entries := []*ImageMetadata{
		{},
		{},
	}
	got := mergeHostRequirements(entries)
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestMergeHostRequirements_SingleEntry(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{CPUs: 4, Memory: "8gb", Storage: "32gb"},
		}},
	}
	got := mergeHostRequirements(entries)
	if got == nil {
		t.Fatal("expected non-nil result")
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
}

func TestMergeHostRequirements_MaxCPUs(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{CPUs: 2},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{CPUs: 8},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{CPUs: 4},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.CPUs != 8 {
		t.Errorf("CPUs = %d, want 8", got.CPUs)
	}
}

func TestMergeHostRequirements_MaxMemory(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Memory: "4gb"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Memory: "16gb"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Memory: "8gb"},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.Memory != "16gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "16gb")
	}
}

func TestMergeHostRequirements_MaxStorage(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Storage: "64gb"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Storage: "1tb"},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.Storage != "1tb" {
		t.Errorf("Storage = %q, want %q", got.Storage, "1tb")
	}
}

func TestMergeHostRequirements_MixedUnits(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Memory: "512mb"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Memory: "1gb"},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.Memory != "1gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "1gb")
	}
}

func TestMergeHostRequirements_GPUTrueBeatsOptional(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{GPU: "optional"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{GPU: "true"},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.GPU != gpuTrue {
		t.Errorf("GPU = %q, want %q", got.GPU, gpuTrue)
	}
}

func TestMergeHostRequirements_GPUOptionalBeatsFalse(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{GPU: "false"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{GPU: "optional"},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.GPU != "optional" {
		t.Errorf("GPU = %q, want %q", got.GPU, "optional")
	}
}

func TestMergeHostRequirements_GPUTrueBeatsFalse(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{GPU: "false"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{GPU: "true"},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.GPU != gpuTrue {
		t.Errorf("GPU = %q, want %q", got.GPU, gpuTrue)
	}
}

func TestMergeHostRequirements_GPUEmptyPreservesValue(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{GPU: "optional"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.GPU != "optional" {
		t.Errorf("GPU = %q, want %q", got.GPU, "optional")
	}
}

func TestMergeHostRequirements_MultiSource(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{
				CPUs:    2,
				Memory:  "4gb",
				Storage: "32gb",
				GPU:     "false",
			},
		}},
		{}, // no requirements
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{CPUs: 4, Memory: "8gb", GPU: "optional"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{
				CPUs:    1,
				Memory:  "16gb",
				Storage: "64gb",
				GPU:     "true",
			},
		}},
	}
	got := mergeHostRequirements(entries)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", got.CPUs)
	}
	if got.Memory != "16gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "16gb")
	}
	if got.Storage != "64gb" {
		t.Errorf("Storage = %q, want %q", got.Storage, "64gb")
	}
	if got.GPU != gpuTrue {
		t.Errorf("GPU = %q, want %q", got.GPU, gpuTrue)
	}
}

func TestMergeHostRequirements_PartialFields(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{CPUs: 4},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Memory: "8gb"},
		}},
		{DevContainerConfigBase: DevContainerConfigBase{
			HostRequirements: &HostRequirements{Storage: "100gb"},
		}},
	}
	got := mergeHostRequirements(entries)
	if got.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", got.CPUs)
	}
	if got.Memory != "8gb" {
		t.Errorf("Memory = %q, want %q", got.Memory, "8gb")
	}
	if got.Storage != "100gb" {
		t.Errorf("Storage = %q, want %q", got.Storage, "100gb")
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
		a, b, want string
	}{
		{"", "", ""},
		{"4gb", "", "4gb"},
		{"", "8gb", "8gb"},
		{"4gb", "8gb", "8gb"},
		{"8gb", "4gb", "8gb"},
		{"512mb", "1gb", "1gb"},
		{"1gb", "512mb", "1gb"},
		{"1tb", "1024gb", "1tb"},
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

func TestGpuStrBoolPriority(t *testing.T) {
	tests := []struct {
		input types.StrBool
		want  int
	}{
		{"", 0},
		{"false", 1},
		{"optional", 2},
		{"true", 3},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got := gpuStrBoolPriority(tt.input)
			if got != tt.want {
				t.Errorf("gpuStrBoolPriority(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestMergeGPU(t *testing.T) {
	tests := []struct {
		name string
		a, b types.StrBool
		want types.StrBool
	}{
		{"empty_empty", "", "", ""},
		{"true_false", "true", "false", "true"},
		{"false_true", "false", "true", "true"},
		{"optional_true", "optional", "true", "true"},
		{"true_optional", "true", "optional", "true"},
		{"false_optional", "false", "optional", "optional"},
		{"optional_false", "optional", "false", "optional"},
		{"empty_true", "", "true", "true"},
		{"true_empty", "true", "", "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeGPU(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("mergeGPU(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
