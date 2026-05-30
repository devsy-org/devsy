package provider

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
)

func TestUpdateCmd_VersionFlag(t *testing.T) {
	cmd := NewUpdateCmd(&flags.GlobalFlags{})
	if cmd.Flag("version") == nil {
		t.Fatal("expected --version flag")
	}
}
