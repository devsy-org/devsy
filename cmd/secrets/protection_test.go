package secrets

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func TestProtectionPassphraseUsesStdin(t *testing.T) {
	command := &cobra.Command{}
	command.SetIn(strings.NewReader("long-passphrase-value\r\n"))
	value, err := readProtectionPassphrase(command, true)
	if err != nil || value != "long-passphrase-value" {
		t.Fatalf("stdin input: %q, %v", value, err)
	}
	command.SetIn(strings.NewReader("\n"))
	if _, err = readProtectionPassphrase(command, true); err == nil {
		t.Fatal("accepted empty passphrase")
	}
}

func TestProtectionNeverRegistersArgvPassphrase(t *testing.T) {
	command := NewProtectionCmd(&flags.GlobalFlags{})
	for _, child := range command.Commands() {
		for _, flag := range []string{"passphrase", "value"} {
			if child.Flags().Lookup(flag) != nil {
				t.Fatalf("%s accepts %s in argv", child.Name(), flag)
			}
		}
	}
	target, _, err := command.Find([]string{"set-passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	if target.Flags().Lookup("stdin") == nil {
		t.Fatal("new passphrase lacks stdin transport")
	}
}

func TestProtectionEmptyPassphraseErrorDoesNotEchoInput(t *testing.T) {
	command := &cobra.Command{}
	command.SetIn(bytes.NewReader(nil))
	_, err := readProtectionPassphrase(command, true)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected safe empty error: %v", err)
	}
}
