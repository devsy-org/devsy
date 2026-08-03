package up

import "testing"

func TestEnsureArgsForFromSnapshot_SynthesizesPlaceholderWhenArgsEmpty(t *testing.T) {
	cmd := &UpCmd{}
	cmd.FromSnapshot = "ghcr.io/acme/snapshots:my-ws-20260731150405-abcxyz"

	args := cmd.ensureArgsForFromSnapshot(nil)

	if len(args) != 1 || args[0] != cmd.FromSnapshot {
		t.Errorf(
			"args = %v, want a single placeholder arg "+
				"(without one, resolveWorkspace never takes its create-new-workspace path)",
			args,
		)
	}
}

func TestEnsureArgsForFromSnapshot_LeavesNonEmptyArgsUntouched(t *testing.T) {
	cmd := &UpCmd{}
	cmd.FromSnapshot = "ghcr.io/acme/snapshots:my-ws-20260731150405-abcxyz"

	args := cmd.ensureArgsForFromSnapshot([]string{"already-set"})

	if len(args) != 1 || args[0] != "already-set" {
		t.Errorf("args = %v, want unchanged [already-set]", args)
	}
}

func TestEnsureArgsForFromSnapshot_NoOpWithoutFromSnapshot(t *testing.T) {
	cmd := &UpCmd{}

	args := cmd.ensureArgsForFromSnapshot(nil)

	if args != nil {
		t.Errorf("args = %v, want nil (no --from-snapshot, nothing to synthesize)", args)
	}
}
