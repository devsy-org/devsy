package machine

import (
	"os"
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
)

func TestDeleteCmd_RemovesMachineDirFromDisk(t *testing.T) {
	pkgconfig.ResetPathManager()
	t.Cleanup(pkgconfig.ResetPathManager)
	home := t.TempDir()
	t.Setenv(pkgconfig.EnvHome, home)

	machineDir, err := pkgconfig.DefaultPathManager().
		MachineDir(pkgconfig.DefaultContext, "test-machine")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(machineDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := removeMachineDirIfPresent(pkgconfig.DefaultContext, "test-machine"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(machineDir); !os.IsNotExist(err) {
		t.Fatalf("expected machine dir %s to be removed, stat err = %v", machineDir, err)
	}
}
