package distinctid

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
)

type machineVector struct {
	Name         string             `json:"name"`
	Platform     string             `json:"platform"`
	NativeInputs map[string]*string `json:"native_inputs"`
	Normalized   string             `json:"normalized_machine_id"`
	Expected     string             `json:"distinct_id"`
}

type overrideVector struct {
	Override   string `json:"override"`
	Normalized string `json:"normalized_machine_id"`
	Expected   string `json:"distinct_id"`
}

type failureVector struct {
	Name         string             `json:"name"`
	Platform     string             `json:"platform"`
	NativeInputs map[string]*string `json:"native_inputs"`
	Seed         *string            `json:"fallback_seed"`
	RandomError  bool               `json:"random_error"`
	Expected     *string            `json:"distinct_id"`
}

func TestSharedIdentityInputs(t *testing.T) {
	data, err := os.ReadFile("../testdata/identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Machine   []machineVector  `json:"machine_vectors"`
		Overrides []overrideVector `json:"override_vectors"`
		Failures  []failureVector  `json:"failure_vectors"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, vector := range fixture.Machine {
		assertMachineVector(t, vector)
	}
	for _, vector := range fixture.Overrides {
		assertOverrideVector(t, vector)
	}
	for _, vector := range fixture.Failures {
		assertFailureVector(t, vector)
	}
}

func assertMachineVector(t *testing.T, vector machineVector) {
	t.Helper()
	source := vector.Normalized
	if vector.Platform == "linux" {
		if v := vector.NativeInputs["dbus_machine_id"]; v != nil && strings.TrimSpace(*v) != "" {
			source = *v
		} else if v := vector.NativeInputs["etc_machine_id"]; v != nil {
			source = *v
		}
	}
	if vector.Platform == "windows" && vector.NativeInputs["machine_guid"] != nil {
		source = *vector.NativeInputs["machine_guid"]
	}
	got := derive(source, nil, func() string { t.Fatal("unexpected fallback"); return "" })
	if got != vector.Expected {
		t.Fatalf("%s identity %q, want %q", vector.Name, got, vector.Expected)
	}
}

func assertOverrideVector(t *testing.T, vector overrideVector) {
	t.Helper()
	if vector.Override == "" {
		if got := derive(
			vector.Normalized,
			nil,
			func() string { return "" },
		); got != vector.Expected {
			t.Fatalf("empty override identity %q, want %q", got, vector.Expected)
		}
	} else {
		t.Setenv(config.EnvTelemetryDistinctID, vector.Override)
		if got := Get(); got != vector.Expected {
			t.Fatalf("override = %q, want %q", got, vector.Expected)
		}
	}
}

func assertFailureVector(t *testing.T, vector failureVector) {
	t.Helper()
	seed := ""
	if vector.Seed != nil {
		seed = *vector.Seed
	}
	source := ""
	nativeErr := errors.New("native unavailable")
	if vector.Platform == "linux" && vector.NativeInputs["dbus_machine_id"] != nil {
		source = *vector.NativeInputs["dbus_machine_id"]
		nativeErr = nil
	}
	got := derive(source, nativeErr, func() string { return seed })
	assertFailureResult(t, vector, got)
}

func assertFailureResult(t *testing.T, vector failureVector, got string) {
	t.Helper()
	if vector.RandomError && (got != "" || vector.Expected != nil) {
		t.Fatal("failed random source must disable identity")
	}
	if !vector.RandomError && (vector.Expected == nil || got != *vector.Expected) {
		t.Fatalf("%s fallback identity mismatch", vector.Name)
	}
}
