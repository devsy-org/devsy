package config

import (
	"testing"
)

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
