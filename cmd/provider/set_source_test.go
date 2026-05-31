package provider

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
)

func TestSetSourceCmd_VersionFlag(t *testing.T) {
	cmd := NewSetSourceCmd(&flags.GlobalFlags{})
	if cmd.Flag("version") == nil {
		t.Fatal("expected --version flag")
	}
}
