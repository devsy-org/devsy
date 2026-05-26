package config

import (
	"encoding/json"
	"fmt"
	"io"
)

type ResultEnvelope struct {
	Outcome               string   `json:"outcome"`
	ContainerID           string   `json:"containerId"`
	RemoteUser            string   `json:"remoteUser"`
	RemoteWorkspaceFolder string   `json:"remoteWorkspaceFolder"`
	URL                   string   `json:"url,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
}

type ErrorEnvelope struct {
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

// WriteResultJSON serializes env as a success envelope to w. The caller
// supplies the envelope fields; this function stamps Outcome="success" and
// appends a trailing newline.
func WriteResultJSON(w io.Writer, env ResultEnvelope) error {
	env.Outcome = "success"
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

func WriteErrorJSON(w io.Writer, msg string) error {
	env := ErrorEnvelope{
		Outcome: "error",
		Message: msg,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}
