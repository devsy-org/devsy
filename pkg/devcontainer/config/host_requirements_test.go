package config

import (
	"fmt"
	"testing"
)

const testWorkspacePath = "/workspace"

type mockHostInfo struct {
	cpus    int
	memory  uint64
	memErr  error
	storage uint64
	storErr error
}

func (m mockHostInfo) NumCPU() int {
	return m.cpus
}

func (m mockHostInfo) TotalMemoryBytes() (uint64, error) {
	return m.memory, m.memErr
}

func (m mockHostInfo) AvailableStorageBytes(_ string) (uint64, error) {
	return m.storage, m.storErr
}

func TestValidateHostRequirements_Nil(t *testing.T) {
	warnings := ValidateHostRequirements(nil, mockHostInfo{}, "/tmp")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for nil reqs, got %v", warnings)
	}
}

func TestValidateHostRequirements_AllMet(t *testing.T) {
	reqs := &HostRequirements{
		CPUs:    2,
		Memory:  "3gb",
		Storage: "10gb",
	}
	host := mockHostInfo{
		cpus:    4,
		memory:  8 * 1024 * 1024 * 1024,
		storage: 50 * 1024 * 1024 * 1024,
	}
	warnings := ValidateHostRequirements(reqs, host, testWorkspacePath)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestValidateHostRequirements_CPUsInsufficient(t *testing.T) {
	reqs := &HostRequirements{CPUs: 8}
	host := mockHostInfo{cpus: 4}
	warnings := ValidateHostRequirements(reqs, host, testWorkspacePath)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if warnings[0] != "cpus: required 8, available 4" {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestValidateHostRequirements_MemoryInsufficient(t *testing.T) {
	reqs := &HostRequirements{Memory: "24gb"}
	host := mockHostInfo{
		cpus:   8,
		memory: 8 * 1024 * 1024 * 1024,
	}
	warnings := ValidateHostRequirements(reqs, host, testWorkspacePath)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	expected := fmt.Sprintf(
		"memory: required 24gb (%d bytes), available %d bytes",
		uint64(24)*1024*1024*1024, uint64(8)*1024*1024*1024,
	)
	if warnings[0] != expected {
		t.Errorf("got %q, want %q", warnings[0], expected)
	}
}

func TestValidateHostRequirements_StorageInsufficient(t *testing.T) {
	reqs := &HostRequirements{Storage: "200gb"}
	host := mockHostInfo{
		cpus:    8,
		memory:  32 * 1024 * 1024 * 1024,
		storage: 20 * 1024 * 1024 * 1024,
	}
	warnings := ValidateHostRequirements(reqs, host, testWorkspacePath)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	expected := fmt.Sprintf(
		"storage: required 200gb (%d bytes), available %d bytes at %q",
		uint64(200)*1024*1024*1024, uint64(20)*1024*1024*1024, testWorkspacePath,
	)
	if warnings[0] != expected {
		t.Errorf("got %q, want %q", warnings[0], expected)
	}
}

func TestValidateHostRequirements_PartialOnlyCPUs(t *testing.T) {
	reqs := &HostRequirements{CPUs: 2}
	host := mockHostInfo{cpus: 4, memory: 0, storage: 0}
	warnings := ValidateHostRequirements(reqs, host, testWorkspacePath)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for partial (cpus met), got %v", warnings)
	}
}

func TestValidateHostRequirements_DetectionErrors(t *testing.T) {
	reqs := &HostRequirements{Memory: "6gb", Storage: "50gb"}
	host := mockHostInfo{
		cpus:    4,
		memErr:  fmt.Errorf("no /proc/meminfo"),
		storErr: fmt.Errorf("permission denied"),
	}
	warnings := ValidateHostRequirements(reqs, host, testWorkspacePath)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestParseSizeToBytes(t *testing.T) {
	tests := []struct {
		input   string
		want    uint64
		wantErr bool
	}{
		{"7gb", 7 * 1024 * 1024 * 1024, false},
		{"768mb", 768 * 1024 * 1024, false},
		{"2tb", 2 * 1024 * 1024 * 1024 * 1024, false},
		{"128kb", 128 * 1024, false},
		{"7GB", 7 * 1024 * 1024 * 1024, false},
		{"5 gb", 5 * 1024 * 1024 * 1024, false},
		{"2048", 2048, false},
		{"", 0, true},
		{"abc", 0, true},
		{"gb", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSizeToBytes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSizeToBytes(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseSizeToBytes(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
