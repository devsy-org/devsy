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
	Warnings              []string `json:"warnings,omitempty"`
}

type ErrorEnvelope struct {
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

//nolint:revive
func WriteResultJSON(w io.Writer, containerID, user, workdir string, warnings []string) error {
	env := ResultEnvelope{
		Outcome:               "success",
		ContainerID:           containerID,
		RemoteUser:            user,
		RemoteWorkspaceFolder: workdir,
		Warnings:              warnings,
	}
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
