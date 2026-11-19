package telemetry

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/telemetry/distinctid"
)

type machineIdentityVector struct {
	ID       string `json:"normalized_machine_id"`
	Expected string `json:"distinct_id"`
}

type workspaceIdentityVector struct {
	UID      string `json:"workspace_uid"`
	Expected string `json:"workspace_id"`
}

func TestSharedIdentityVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version   int                       `json:"version"`
		Machine   []machineIdentityVector   `json:"machine_vectors"`
		Workspace []workspaceIdentityVector `json:"workspace_vectors"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != 1 {
		t.Fatal("unsupported fixture version")
	}
	for _, vector := range fixture.Machine {
		if got := distinctid.Hash("machine", vector.ID); got != vector.Expected {
			t.Fatalf("machine identity %q, want %q", got, vector.Expected)
		}
	}
	for _, vector := range fixture.Workspace {
		if got := hashScopedID("unused", vector.UID); got != vector.Expected {
			t.Fatalf("workspace identity %q, want %q", got, vector.Expected)
		}
	}
}
