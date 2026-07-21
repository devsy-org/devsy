package mcp

import (
	"github.com/devsy-org/devsy/pkg/clierr"
)

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ClassifyError(err error) ErrorPayload {
	classified := clierr.Classify(err)
	if classified == nil {
		return ErrorPayload{}
	}
	return ErrorPayload{
		Code:    string(classified.Code),
		Message: classified.Message,
	}
}
