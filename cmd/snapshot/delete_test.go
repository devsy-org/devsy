package snapshot

import "testing"

func TestDeleteCmd_RequiresValidRef(t *testing.T) {
	cmd := &DeleteCmd{}
	if err := cmd.validateRef("not-a-ref"); err == nil {
		t.Fatal("expected error for invalid snapshot ref")
	}
	if err := cmd.validateRef("ghcr.io/acme/s:my-ws-20260731150405-abcxyz"); err != nil {
		t.Fatalf("unexpected error for valid ref: %v", err)
	}
}
