package log

import (
	"github.com/devsy-org/devsy/pkg/survey"
)

// QuestionDefault asks a question using the default survey implementation.
func QuestionDefault(opts *survey.QuestionOptions) (string, error) {
	return survey.NewSurvey().Question(opts)
}
