package mcp

import (
	"errors"

	cliErrors "github.com/devsy-org/devsy/pkg/errors"
	"github.com/devsy-org/devsy/pkg/workspace"
)

// ErrorPayload is the JSON shape attached to MCP tool errors so the agent gets
// the same structured information the Devsy CLI shows humans (code, hint, doc URL).
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	DocURL  string `json:"doc_url,omitempty"`
}

// ClassifyError converts any error returned by an MCP handler into a structured
// payload using the same classifier the CLI uses.
func ClassifyError(err error) ErrorPayload {
	if err == nil {
		return ErrorPayload{}
	}
	if errors.Is(err, workspace.ErrWorkspaceNotFound) {
		return ErrorPayload{
			Code:    "workspace_not_found",
			Message: err.Error(),
		}
	}
	classified := cliErrors.Classify(err, cliErrors.ClassifyContext{})
	code := "internal_error"
	if classified.Code != "" {
		code = string(classified.Code)
	}
	return ErrorPayload{
		Code:    code,
		Message: classified.Message,
		Hint:    classified.Hint,
		DocURL:  classified.DocURL,
	}
}
