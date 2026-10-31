package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	prebuildTestAmd64 = "amd64"
	prebuildTestArm64 = "arm64"
	prebuildTestName = "test"
)

func TestNormalizeArchitecture(t *testing.T) {
	tests := []struct {
		name         string
		platform     string
		architecture string
		want         string
	}{
		{
			name:         "extract from linux platform",
			platform:     "linux/amd64",
			architecture: prebuildTestArm64,
			want:         prebuildTestAmd64,
		},
		{
			name:         "extract from linux arm64",
			platform:     "linux/arm64",
			architecture: prebuildTestAmd64,
			want:         prebuildTestArm64,
		},
		{
			name:         "empty platform uses architecture",
			platform:     "",
			architecture: prebuildTestAmd64,
			want:         prebuildTestAmd64,
		},
		{
			name:         "non-linux platform uses architecture",
			platform:     "windows/amd64",
			architecture: prebuildTestArm64,
			want:         prebuildTestArm64,
		},
		{
			name:         "invalid platform format uses architecture",
			platform:     "linux",
			architecture: prebuildTestAmd64,
			want:         prebuildTestAmd64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeArchitecture(tt.platform, tt.architecture)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeConfigForHash(t *testing.T) {
	t.Run("clears non-build fields", func(t *testing.T) {
		config := &DevContainerConfig{
			Origin: "/some/path",
			DevContainerConfigBase: DevContainerConfigBase{
				Name: "test-container",
				Features: map[string]any{
					"ghcr.io/devcontainers/features/go": "latest",
				},
			},
			ImageContainer: ImageContainer{
				Image: "ubuntu:22.04",
			},
			DockerfileContainer: DockerfileContainer{
				Dockerfile: "Dockerfile",
				Context:    ".",
			},
		}

		configJSON, err := normalizeConfigForHash(config)
		require.NoError(t, err)

		var normalized DevContainerConfig
		err = json.Unmarshal(configJSON, &normalized)
		require.NoError(t, err)

		// Origin should be cleared
		assert.Empty(t, normalized.Origin)

		// Build-relevant fields should be preserved
		assert.Equal(t, "test-container", normalized.Name)
		assert.NotEmpty(t, normalized.Features)
		assert.Equal(t, "ubuntu:22.04", normalized.Image)
		assert.Equal(t, "Dockerfile", normalized.Dockerfile)
	})

	t.Run("deterministic output", func(t *testing.T) {
		config := &DevContainerConfig{
			DevContainerConfigBase: DevContainerConfigBase{
				Name: prebuildTestName,
			},
		}

		json1, err := normalizeConfigForHash(config)
		require.NoError(t, err)

		json2, err := normalizeConfigForHash(config)
		require.NoError(t, err)

		assert.Equal(t, json1, json2)
	})
}

func TestReadDockerignore(t *testing.T) {
	t.Run("reads patterns from .dockerignore", func(t *testing.T) {
		tempDir := t.TempDir()

		dockerignorePath := filepath.Join(tempDir, ".dockerignore")
		err := os.WriteFile(dockerignorePath, []byte("*.log\nnode_modules\n"), 0o600)
		require.NoError(t, err)

		excludes, err := readDockerignore(tempDir, "Dockerfile")
		require.NoError(t, err)

		assert.Contains(t, excludes, "*.log")
		assert.Contains(t, excludes, "node_modules")
	})

	t.Run("works without .dockerignore", func(t *testing.T) {
		tempDir := t.TempDir()

		excludes, err := readDockerignore(tempDir, "Dockerfile")
		require.NoError(t, err)
		assert.NotNil(t, excludes)
	})
}
