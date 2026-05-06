package output

import "github.com/devsy-org/devsy/pkg/terminal"

const (
	ModeJSON  = "json"
	ModePlain = "plain"
)

func ResolveMode(flagValue string) string {
	switch flagValue {
	case ModeJSON:
		return ModeJSON
	case ModePlain:
		return ModePlain
	case "auto":
		if !terminal.IsTerminalOut {
			return ModeJSON
		}
		return ModePlain
	default:
		return ModeJSON
	}
}
