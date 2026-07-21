package analytics

import "testing"

func TestBuildProperties_MergesEventAndUserWithoutCollision(t *testing.T) {
	event := Event{
		"event": {
			KeyType:      "devsy_workspace_count",
			KeyMachineID: "m",
			KeyTimestamp: int64(1),
			"count":      7,
			"version":    "1.2.3",
		},
		"user": {
			KeyMachineID: "m",
			KeyTimestamp: int64(1),
			"os_name":    "darwin",
			"os_arch":    "arm64",
		},
	}

	got := buildProperties(event)

	want := map[string]any{
		"count":   7,
		"version": "1.2.3",
		"os_name": "darwin",
		"os_arch": "arm64",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("property %q = %v, want %v", k, got[k], v)
		}
	}
}

func TestBuildProperties_ExcludesReservedKeys(t *testing.T) {
	event := Event{
		"event": {
			KeyType:      "devsy_cli",
			KeyMachineID: "m",
			KeyTimestamp: int64(1),
			"command":    "up",
		},
		"user": {
			KeyMachineID: "m",
			KeyTimestamp: int64(1),
		},
	}

	got := buildProperties(event)

	for _, reserved := range []string{KeyType, KeyMachineID, KeyTimestamp} {
		if _, ok := got[reserved]; ok {
			t.Errorf("reserved key %q should not appear in properties", reserved)
		}
	}
	if got["command"] != "up" {
		t.Errorf("command = %v, want up", got["command"])
	}
}
