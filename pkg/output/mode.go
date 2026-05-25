package output

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/terminal"
)

const (
	ModeJSON  = "json"
	ModePlain = "plain"
)

func ResolveMode(flagValue string) (string, error) {
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
