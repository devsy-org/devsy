package config

import (
	"bytes"
	"encoding/json"
	"testing"
)

//nolint:goconst,cyclop,funlen // test table values
func TestWriteResultJSON(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		user        string
		workdir     string
	}{
		{
			name:        "typical values",
			containerID: "abc123def456",
			user:        "vscode",
			workdir:     "/workspaces/project",
		},
		{
			name:        "root user",
			containerID: "sha256:abcdef1234567890",
			user:        "root",
			workdir:     "/workspaces/my-app",
		},
		{
			name:        "empty strings",
			containerID: "",
			user:        "",
			workdir:     "",
		},
		{
			name:        "special characters in container ID",
			containerID: "container:with/special-chars_123",
			user:        "dev-user",
			workdir:     "/workspaces/my project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteResultJSON(&buf, tt.containerID, tt.user, tt.workdir)
			if err != nil {
				t.Fatalf("WriteResultJSON returned error: %v", err)
			}

			output := buf.Bytes()
			if output[len(output)-1] != '\n' {
				t.Fatal("output must be newline-terminated")
			}

			var envelope ResultEnvelope
			if err := json.Unmarshal(output, &envelope); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}

			if envelope.Outcome != "success" {
				t.Errorf("outcome = %q, want %q", envelope.Outcome, "success")
			}
			if envelope.ContainerID != tt.containerID {
				t.Errorf("containerId = %q, want %q", envelope.ContainerID, tt.containerID)
			}
			if envelope.RemoteUser != tt.user {
				t.Errorf("remoteUser = %q, want %q", envelope.RemoteUser, tt.user)
			}
			if envelope.RemoteWorkspaceFolder != tt.workdir {
				t.Errorf("remoteWorkspaceFolder = %q, want %q",
					envelope.RemoteWorkspaceFolder, tt.workdir)
			}

			if bytes.Contains(output[:len(output)-1], []byte("\n")) {
				t.Error("JSON must be single-line (no embedded newlines)")
			}
		})
	}
}

func TestWriteErrorJSON(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "simple error",
			message: "container failed to start",
		},
		{
			name:    "empty message",
			message: "",
		},
		{
			name:    "message with quotes",
			message: `failed to parse "devcontainer.json": unexpected token`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteErrorJSON(&buf, tt.message)
			if err != nil {
				t.Fatalf("WriteErrorJSON returned error: %v", err)
			}

			output := buf.Bytes()
			if output[len(output)-1] != '\n' {
				t.Fatal("output must be newline-terminated")
			}

			var envelope ErrorEnvelope
			if err := json.Unmarshal(output, &envelope); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}

			if envelope.Outcome != "error" {
				t.Errorf("outcome = %q, want %q", envelope.Outcome, "error")
			}
			if envelope.Message != tt.message {
				t.Errorf("message = %q, want %q", envelope.Message, tt.message)
			}

			if bytes.Contains(output[:len(output)-1], []byte("\n")) {
				t.Error("JSON must be single-line (no embedded newlines)")
			}
		})
	}
}
