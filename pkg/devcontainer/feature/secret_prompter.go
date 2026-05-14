package feature

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/survey"
	"github.com/devsy-org/devsy/pkg/terminal"
)

// TerminalSecretPrompter prompts for secrets interactively when stdin is a
// terminal, and emits a warning in non-interactive mode.
type TerminalSecretPrompter struct {
	IsTerminal func() bool
}

func (p *TerminalSecretPrompter) PromptSecret(featureID, optionName string) (string, error) {
	if !p.isTerminal() {
		log.Warnf(
			"Feature %q: secret option %q has no value "+
				"and stdin is not a terminal; skipping prompt",
			featureID,
			optionName,
		)
		return "", nil
	}

	question := fmt.Sprintf(
		"Enter value for secret option %q in feature %q",
		optionName, featureID,
	)
	answer, err := log.QuestionDefault(&survey.QuestionOptions{
		Question:   question,
		IsPassword: true,
	})
	if err != nil {
		return "", err
	}

	return answer, nil
}

func (p *TerminalSecretPrompter) isTerminal() bool {
	if p.IsTerminal != nil {
		return p.IsTerminal()
	}
	return terminal.IsTerminalIn
}
