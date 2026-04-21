package log

import (
	"github.com/devsy-org/devsy/pkg/survey"
	oldlog "github.com/devsy-org/log"
	oldsurvey "github.com/devsy-org/log/survey"
)

// QuestionDefault asks a question using the default logger, bridging the local
// survey type to the old log module's survey type during the migration.
func QuestionDefault(opts *survey.QuestionOptions) (string, error) {
	return oldlog.Default.Question(convertOpts(opts))
}

// QuestionWith asks a question using the given logger, bridging the local
// survey type to the old log module's survey type during the migration.
func QuestionWith(logger oldlog.Logger, opts *survey.QuestionOptions) (string, error) {
	return logger.Question(convertOpts(opts))
}

func convertOpts(opts *survey.QuestionOptions) *oldsurvey.QuestionOptions {
	converted := oldsurvey.QuestionOptions(*opts)
	return &converted
}
