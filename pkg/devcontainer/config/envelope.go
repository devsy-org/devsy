package config

import (
	"encoding/json"
	"fmt"
	"io"
)

type ResultEnvelope struct {
	Outcome               string `json:"outcome"`
	ContainerID           string `json:"containerId"`
	RemoteUser            string `json:"remoteUser"`
	RemoteWorkspaceFolder string `json:"remoteWorkspaceFolder"`
}

type ErrorEnvelope struct {
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

func WriteResultJSON(w io.Writer, containerID, user, workdir string) error {
	env := ResultEnvelope{
		Outcome:               "success",
		ContainerID:           containerID,
		RemoteUser:            user,
		RemoteWorkspaceFolder: workdir,
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
