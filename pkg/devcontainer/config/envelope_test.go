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
		warnings    []string
	}{
		{
			name:        "typical values",
			containerID: "abc123def456",
			user:        "vscode",
			workdir:     "/workspaces/project",
			warnings:    nil,
		},
		{
			name:        "root user",
			containerID: "sha256:abcdef1234567890",
			user:        "root",
			workdir:     "/workspaces/my-app",
			warnings:    nil,
		},
		{
			name:        "empty strings",
			containerID: "",
			user:        "",
			workdir:     "",
			warnings:    nil,
		},
		{
			name:        "special characters in container ID",
			containerID: "container:with/special-chars_123",
			user:        "dev-user",
			workdir:     "/workspaces/my project",
			warnings:    nil,
		},
		{
			name:        "with warnings",
			containerID: "abc123",
			user:        "vscode",
			workdir:     "/workspaces/project",
			warnings: []string{
				"cpus: required 128, available 4",
				"memory: required 256gb (274877906944 bytes), available 17179869184 bytes",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteResultJSON(&buf, tt.containerID, tt.user, tt.workdir, tt.warnings)
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
			if len(tt.warnings) == 0 {
				if envelope.Warnings != nil {
					t.Errorf("warnings = %v, want nil (omitempty)", envelope.Warnings)
				}
				if bytes.Contains(output, []byte(`"warnings"`)) {
					t.Error("warnings field must be omitted when empty")
				}
			}
			if len(tt.warnings) > 0 {
				if len(envelope.Warnings) != len(tt.warnings) {
					t.Fatalf(
						"warnings length = %d, want %d",
						len(envelope.Warnings), len(tt.warnings),
					)
				}
				for i, w := range tt.warnings {
					if envelope.Warnings[i] != w {
						t.Errorf("warnings[%d] = %q, want %q", i, envelope.Warnings[i], w)
					}
				}
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
