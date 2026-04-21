package survey

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	surveypkg "github.com/AlecAivazis/survey/v2"
)

// QuestionOptions defines a question and its options.
type QuestionOptions struct {
	Question               string
	DefaultValue           string
	DefaultValueSet        bool
	ValidationRegexPattern string
	ValidationMessage      string
	ValidationFunc         func(value string) error
	Options                []string
	Sort                   bool
	IsPassword             bool
}

// DefaultValidationRegexPattern is the default regex pattern to validate the input.
var DefaultValidationRegexPattern = regexp.MustCompile("^.*$")

// Survey is the interface for asking questions.
type Survey interface {
	Question(params *QuestionOptions) (string, error)
}

type survey struct{}

// NewSurvey creates a new survey object.
func NewSurvey() Survey {
	return &survey{}
}

// Question asks the user a question and returns the answer.
func (s *survey) Question(params *QuestionOptions) (string, error) {
	compiledRegex := DefaultValidationRegexPattern
	if params.ValidationRegexPattern != "" {
		compiledRegex = regexp.MustCompile(params.ValidationRegexPattern)
	}

	prompt := buildPrompt(params)
	question := []*surveypkg.Question{
		{
			Name:   "question",
			Prompt: prompt,
		},
	}

	if params.Options == nil {
		question[0].Validate = buildValidator(params, compiledRegex)
	}

	answers := struct {
		Question string
	}{}

	err := surveypkg.Ask(question, &answers)
	if err != nil {
		return "", err
	}
	if answers.Question == "" && len(params.Options) > 0 {
		answers.Question = params.Options[0]
	}

	return answers.Question, nil
}

func buildPrompt(params *QuestionOptions) surveypkg.Prompt {
	switch {
	case params.Options != nil:
		if params.Sort {
			params.Options = copyStringArray(params.Options)
			sort.Strings(params.Options)
		}

		var defaultValue any
		if params.DefaultValue != "" {
			defaultValue = params.DefaultValue
		}

		return &surveypkg.Select{
			Message: params.Question,
			Options: params.Options,
			Default: defaultValue,
		}
	case params.IsPassword:
		return &surveypkg.Password{
			Message: params.Question,
		}
	default:
		return &surveypkg.Input{
			Message: params.Question,
			Default: params.DefaultValue,
		}
	}
}

func buildValidator(params *QuestionOptions, compiledRegex *regexp.Regexp) func(val any) error {
	return func(val any) error {
		str, ok := val.(string)
		if !ok {
			return errors.New("input was not a string")
		}

		if !compiledRegex.MatchString(str) {
			if params.ValidationMessage != "" {
				return errors.New(params.ValidationMessage)
			}

			return fmt.Errorf("answer has to match pattern: %s", compiledRegex.String())
		}

		if params.ValidationFunc != nil {
			if err := params.ValidationFunc(str); err != nil {
				if params.ValidationMessage != "" {
					return errors.New(params.ValidationMessage)
				}

				return fmt.Errorf("%v", err)
			}
		}

		return nil
	}
}

func copyStringArray(strings []string) []string {
	retStrings := []string{}
	retStrings = append(retStrings, strings...)
	return retStrings
}
