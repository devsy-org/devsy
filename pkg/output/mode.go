package output

import (
	"fmt"
	"os"

	"github.com/devsy-org/devsy/pkg/terminal"
)

const (
	ModeJSON  = "json"
	ModePlain = "plain"
)

func ResolveMode(flagValue string) (string, error) {
	// The desktop always consumes structured output, even when its child
	// process happens to inherit a terminal. This keeps the protocol decision
	// independent of the host application's launch environment.
	if os.Getenv("DEVSY_UI") == "true" {
		return ModeJSON, nil
	}

	switch flagValue {
	case ModeJSON:
		return ModeJSON, nil
	case ModePlain:
		return ModePlain, nil
	case "auto":
		if !terminal.IsTerminalOut {
			return ModeJSON, nil
		}
		return ModePlain, nil
	default:
		return "", fmt.Errorf(
			"unexpected output format, choose json, plain, or auto. Got %q",
			flagValue,
		)
	}
}
