package output

import (
	"errors"
	"os"

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
	case ModePlain, "text":
		return ModePlain, nil
	case "auto", "":
		if os.Getenv("DEVSY_UI") == "true" {
			return ModeJSON, nil
		}
		if !terminal.IsTerminalOut {
			return ModeJSON, nil
		}
		return ModePlain, nil
	default:
		return "", errors.New("unexpected output format, choose auto, text, plain, or json")
	}
}
