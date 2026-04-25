package config

import (
	"encoding/json"
	"testing"
)

func TestGPURequirement_BoolAndString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantVal string
	}{
		{"true", `{"gpu": true}`, "true"},
		{"false", `{"gpu": false}`, "false"},
		{"optional", `{"gpu": "optional"}`, "optional"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hr HostRequirements
			if err := json.Unmarshal([]byte(tt.input), &hr); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if hr.GPU == nil {
				t.Fatal("GPU is nil")
			}
			if hr.GPU.Value != tt.wantVal {
				t.Errorf("Value = %q, want %q", hr.GPU.Value, tt.wantVal)
			}
		})
	}
}

func TestGPURequirement_ObjectFormat(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantVal   string
		wantCores int
		wantMem   string
	}{
		{
			"cores and memory",
			`{"gpu": {"cores": 4, "memory": "8gb"}}`,
			"true", 4, "8gb",
		},
		{
			"cores only",
			`{"gpu": {"cores": 2}}`,
			"true", 2, "",
		},
		{
			"memory only",
			`{"gpu": {"memory": "16gb"}}`,
			"true", 0, "16gb",
		},
		{
			"empty object",
			`{"gpu": {}}`,
			"true", 0, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hr HostRequirements
			if err := json.Unmarshal([]byte(tt.input), &hr); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if hr.GPU == nil {
				t.Fatal("GPU is nil")
			}
			if hr.GPU.Value != tt.wantVal {
				t.Errorf("Value = %q, want %q", hr.GPU.Value, tt.wantVal)
			}
			if hr.GPU.Cores != tt.wantCores {
				t.Errorf("Cores = %d, want %d", hr.GPU.Cores, tt.wantCores)
			}
			if hr.GPU.GPUMemory != tt.wantMem {
				t.Errorf("GPUMemory = %q, want %q", hr.GPU.GPUMemory, tt.wantMem)
			}
		})
	}
}

func TestGPURequirement_InvalidType(t *testing.T) {
	var hr HostRequirements
	err := json.Unmarshal([]byte(`{"gpu": [1, 2, 3]}`), &hr)
	if err == nil {
		t.Fatal("expected error for array input, got nil")
	}
}

func TestGPURequirement_OmittedIsNil(t *testing.T) {
	var hr HostRequirements
	if err := json.Unmarshal([]byte(`{"cpus": 4}`), &hr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if hr.GPU != nil {
		t.Errorf("GPU = %+v, want nil", hr.GPU)
	}
}

func TestShouldEnableGPU(t *testing.T) {
	gpu := func(val string) *GPURequirement { return &GPURequirement{Value: val} }

	tests := []struct {
		name        string
		hr          *HostRequirements
		available   bool
		wantEnable  bool
		wantWarning bool
	}{
		{"nil GPU", &HostRequirements{}, false, false, false},
		{"true available", &HostRequirements{GPU: gpu("true")}, true, true, false},
		{"true unavailable", &HostRequirements{GPU: gpu("true")}, false, false, true},
		{"false", &HostRequirements{GPU: gpu("false")}, true, false, false},
		{"optional available", &HostRequirements{GPU: gpu("optional")}, true, true, false},
		{"optional unavailable", &HostRequirements{GPU: gpu("optional")}, false, false, false},
		{
			"object available",
			&HostRequirements{GPU: &GPURequirement{Value: "true", Cores: 4, GPUMemory: "8gb"}},
			true, true, false,
		},
		{
			"object unavailable",
			&HostRequirements{GPU: &GPURequirement{Value: "true", Cores: 4, GPUMemory: "8gb"}},
			false, false, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enable, warn := tt.hr.ShouldEnableGPU(tt.available)
			if enable != tt.wantEnable {
				t.Errorf("enable = %v, want %v", enable, tt.wantEnable)
			}
			if warn != tt.wantWarning {
				t.Errorf("warnIfMissing = %v, want %v", warn, tt.wantWarning)
			}
		})
	}
}

func TestShouldEnableGPU_NilReceiver(t *testing.T) {
	var hr *HostRequirements
	enable, warn := hr.ShouldEnableGPU(true)
	if enable || warn {
		t.Errorf("got (%v, %v), want (false, false)", enable, warn)
	}
}
