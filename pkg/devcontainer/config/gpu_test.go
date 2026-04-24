package config

import (
	"encoding/json"
	"testing"
)

func unmarshalGPU(t *testing.T, input string) (*HostRequirements, error) {
	t.Helper()
	var hr HostRequirements
	err := json.Unmarshal([]byte(input), &hr)
	return &hr, err
}

func assertGPU(t *testing.T, got *GPURequirement, want GPURequirement) {
	t.Helper()
	if got == nil {
		t.Fatal("expected GPU to be non-nil")
	}
	if *got != want {
		t.Errorf("GPU = %+v, want %+v", *got, want)
	}
}

func TestGPURequirement_BoolAndString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  GPURequirement
	}{
		{"bool true", `{"gpu": true}`, GPURequirement{Value: "true"}},
		{"bool false", `{"gpu": false}`, GPURequirement{Value: "false"}},
		{"string optional", `{"gpu": "optional"}`, GPURequirement{Value: "optional"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr, err := unmarshalGPU(t, tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertGPU(t, hr.GPU, tt.want)
		})
	}
}

func TestGPURequirement_ObjectFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  GPURequirement
	}{
		{
			"cores and memory",
			`{"gpu": {"cores": 2, "memory": "8gb"}}`,
			GPURequirement{Value: "true", Cores: 2, GPUMemory: "8gb"},
		},
		{
			"cores only",
			`{"gpu": {"cores": 4}}`,
			GPURequirement{Value: "true", Cores: 4},
		},
		{
			"memory only",
			`{"gpu": {"memory": "16gb"}}`,
			GPURequirement{Value: "true", GPUMemory: "16gb"},
		},
		{
			"empty object",
			`{"gpu": {}}`,
			GPURequirement{Value: "true"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr, err := unmarshalGPU(t, tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertGPU(t, hr.GPU, tt.want)
		})
	}
}

func TestGPURequirement_InvalidType(t *testing.T) {
	_, err := unmarshalGPU(t, `{"gpu": [1, 2]}`)
	if err == nil {
		t.Fatal("expected error for array type, got nil")
	}
}

func TestGPURequirement_OmittedIsNil(t *testing.T) {
	hr, err := unmarshalGPU(t, `{"cpus": 4}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hr.GPU != nil {
		t.Errorf(
			"expected GPU to be nil when omitted, got %+v",
			hr.GPU,
		)
	}
}

func TestShouldEnableGPU(t *testing.T) {
	tests := []struct {
		name         string
		gpu          *GPURequirement
		gpuAvailable bool
		wantEnable   bool
		wantWarn     bool
	}{
		{"nil GPU", nil, false, false, false},
		{"true avail", &GPURequirement{Value: "true"}, true, true, false},
		{"true unavail", &GPURequirement{Value: "true"}, false, false, true},
		{"false", &GPURequirement{Value: "false"}, false, false, false},
		{"optional avail", &GPURequirement{Value: "optional"}, true, true, false},
		{"optional unavail", &GPURequirement{Value: "optional"}, false, false, false},
		{
			"object avail",
			&GPURequirement{Value: "true", Cores: 2, GPUMemory: "8gb"},
			true, true, false,
		},
		{
			"object unavail",
			&GPURequirement{Value: "true", Cores: 2, GPUMemory: "8gb"},
			false, false, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr := &HostRequirements{GPU: tt.gpu}
			enable, warn := hr.ShouldEnableGPU(tt.gpuAvailable)
			if enable != tt.wantEnable {
				t.Errorf("enable = %v, want %v", enable, tt.wantEnable)
			}
			if warn != tt.wantWarn {
				t.Errorf("warn = %v, want %v", warn, tt.wantWarn)
			}
		})
	}
}

func TestShouldEnableGPU_NilReceiver(t *testing.T) {
	var hr *HostRequirements
	enable, warn := hr.ShouldEnableGPU(true)
	if enable || warn {
		t.Errorf(
			"expected (false, false) for nil receiver, got (%v, %v)",
			enable,
			warn,
		)
	}
}
