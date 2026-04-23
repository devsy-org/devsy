package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretsRoundTrip(t *testing.T) {
	t.Run("parse secrets from devcontainer.json", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "devcontainer.json")
		raw := `{
			"name": "test-secrets",
			"secrets": {
				"MY_TOKEN": {
					"description": "API token",
					"documentationUrl": "https://example.com/docs"
				},
				"EMPTY_SECRET": {}
			}
		}`
		if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := ParseDevContainerJSONFile(configPath)
		if err != nil {
			t.Fatal(err)
		}

		if len(cfg.Secrets) != 2 {
			t.Fatalf("expected 2 secrets, got %d", len(cfg.Secrets))
		}
		myToken, ok := cfg.Secrets["MY_TOKEN"]
		if !ok {
			t.Fatal("expected MY_TOKEN secret")
		}
		if myToken.Description != "API token" {
			t.Errorf("expected description 'API token', got %q", myToken.Description)
		}
		if myToken.DocumentationUrl != "https://example.com/docs" {
			t.Errorf("expected documentationUrl 'https://example.com/docs', got %q", myToken.DocumentationUrl)
		}
		empty, ok := cfg.Secrets["EMPTY_SECRET"]
		if !ok {
			t.Fatal("expected EMPTY_SECRET secret")
		}
		if empty.Description != "" || empty.DocumentationUrl != "" {
			t.Errorf("expected empty SecretConfig, got %+v", empty)
		}
	})

	t.Run("save and reload secrets", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &DevContainerConfig{
			DevContainerConfigBase: DevContainerConfigBase{
				Name: "round-trip",
				Secrets: map[string]SecretConfig{
					"DB_PASSWORD": {Description: "database password"},
				},
			},
		}
		cfg.Origin = filepath.Join(tmpDir, "devcontainer.json")

		if err := SaveDevContainerJSON(cfg); err != nil {
			t.Fatal(err)
		}

		loaded, err := ParseDevContainerJSONFile(cfg.Origin)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded.Secrets) != 1 {
			t.Fatalf("expected 1 secret after round-trip, got %d", len(loaded.Secrets))
		}
		if loaded.Secrets["DB_PASSWORD"].Description != "database password" {
			t.Errorf("expected 'database password', got %q", loaded.Secrets["DB_PASSWORD"].Description)
		}
	})
}

func TestSaveDevContainerJSON(t *testing.T) {
	type args struct {
		config *DevContainerConfig
	}
	tests := []struct {
		name     string
		args     args
		wantErr  bool
		wantJSON string
	}{
		{
			name: "test omit build field in devcontainer.json",
			args: args{
				config: &DevContainerConfig{
					ImageContainer: ImageContainer{
						Image: "test",
					},
				},
			},
			wantErr:  false,
			wantJSON: `{"image":"test"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp(os.TempDir(), "test-devcontainer")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			tt.args.config.Origin = filepath.Join(tmpDir, "devcontainer.json")

			if err := SaveDevContainerJSON(tt.args.config); (err != nil) != tt.wantErr {
				t.Errorf("SaveDevContainerJSON() error = %v, wantErr %v", err, tt.wantErr)
			}

			contents, err := os.ReadFile(tt.args.config.Origin)
			if err != nil {
				t.Fatalf("Failed to read file contents: %v", err)
			}
			if string(contents) != tt.wantJSON {
				t.Errorf("Expected JSON = %v, got %v", tt.wantJSON, string(contents))
			}
		})
	}
}

func TestFindDevContainerConfigs(t *testing.T) {
	tmpDir := t.TempDir()

	configs := []string{
		".devcontainer/python/devcontainer.json",
		".devcontainer/node/devcontainer.json",
	}

	for _, cfg := range configs {
		fullPath := filepath.Join(tmpDir, cfg)
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(fullPath, []byte(`{"name":"test"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	found, err := findDevContainerConfigs(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(found) != 2 {
		t.Errorf("expected 2 configs, got %d", len(found))
	}
}

func TestListDevContainerIDs(t *testing.T) {
	tmpDir := t.TempDir()

	configs := map[string]string{
		".devcontainer/python/devcontainer.json": `{"name":"Python"}`,
		".devcontainer/node/devcontainer.json":   `{"name":"Node"}`,
	}

	for cfg, content := range configs {
		fullPath := filepath.Join(tmpDir, cfg)
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := ListDevContainerIDs(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(ids) != 2 {
		t.Errorf("expected 2 IDs, got %d: %v", len(ids), ids)
	}

	hasNode, hasPython := false, false
	for _, id := range ids {
		if id == "node" {
			hasNode = true
		}
		if id == "python" {
			hasPython = true
		}
	}

	if !hasNode || !hasPython {
		t.Errorf("expected 'node' and 'python' IDs, got: %v", ids)
	}
}

func TestParseDevContainerJSONWithSelector(t *testing.T) {
	t.Run("explicit path", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "custom.json")
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(configPath, []byte(`{"name":"Custom"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		config, err := ParseDevContainerJSONWithSelector(tmpDir, "custom.json", nil)
		if err != nil {
			t.Fatal(err)
		}
		if config.Name != "Custom" {
			t.Errorf("expected Custom, got %s", config.Name)
		}
	})

	t.Run("explicit path not found", func(t *testing.T) {
		tmpDir := t.TempDir()
		_, err := ParseDevContainerJSONWithSelector(tmpDir, "missing.json", nil)
		if err == nil {
			t.Error("expected error for missing file")
		}
	})

	t.Run(".devcontainer/devcontainer.json", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, ".devcontainer", "devcontainer.json")
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(configPath, []byte(`{"name":"Standard"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		config, err := ParseDevContainerJSONWithSelector(tmpDir, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if config.Name != "Standard" {
			t.Errorf("expected Standard, got %s", config.Name)
		}
	})

	t.Run(".devcontainer.json", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, ".devcontainer.json")
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(configPath, []byte(`{"name":"Root"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		config, err := ParseDevContainerJSONWithSelector(tmpDir, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if config.Name != "Root" {
			t.Errorf("expected Root, got %s", config.Name)
		}
	})

	t.Run("single subfolder", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, ".devcontainer/python/devcontainer.json")
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(configPath, []byte(`{"name":"Python"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		config, err := ParseDevContainerJSONWithSelector(tmpDir, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if config.Name != "Python" {
			t.Errorf("expected Python, got %s", config.Name)
		}
	})

	t.Run("multiple subfolders with selector", func(t *testing.T) {
		tmpDir := t.TempDir()
		pythonPath := filepath.Join(tmpDir, ".devcontainer/python/devcontainer.json")
		nodePath := filepath.Join(tmpDir, ".devcontainer/node/devcontainer.json")

		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(pythonPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(nodePath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(pythonPath, []byte(`{"name":"Python"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(nodePath, []byte(`{"name":"Node"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		config, err := ParseDevContainerJSONWithSelector(
			tmpDir,
			"",
			func(matches []string) (string, error) {
				for _, match := range matches {
					if filepath.Base(filepath.Dir(match)) == "python" {
						return match, nil
					}
				}
				return "", errors.New("not found")
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if config.Name != "Python" {
			t.Errorf("expected Python, got %s", config.Name)
		}
	})

	t.Run("multiple subfolders without selector", func(t *testing.T) {
		tmpDir := t.TempDir()
		pythonPath := filepath.Join(tmpDir, ".devcontainer/python/devcontainer.json")
		nodePath := filepath.Join(tmpDir, ".devcontainer/node/devcontainer.json")

		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(pythonPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(nodePath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(pythonPath, []byte(`{"name":"Python"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(nodePath, []byte(`{"name":"Node"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		config, err := ParseDevContainerJSONWithSelector(tmpDir, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if config == nil {
			t.Error("expected config, got nil")
		}
	})

	t.Run("selector error", func(t *testing.T) {
		tmpDir := t.TempDir()
		pythonPath := filepath.Join(tmpDir, ".devcontainer/python/devcontainer.json")
		nodePath := filepath.Join(tmpDir, ".devcontainer/node/devcontainer.json")
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(pythonPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(filepath.Dir(nodePath), 0o755); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(pythonPath, []byte(`{"name":"Python"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.WriteFile(nodePath, []byte(`{"name":"Node"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := ParseDevContainerJSONWithSelector(
			tmpDir,
			"",
			func(matches []string) (string, error) {
				return "", errors.New("selector failed")
			},
		)
		if err == nil || err.Error() != "selector failed" {
			t.Errorf("expected selector error, got %v", err)
		}
	})

	t.Run("no config found", func(t *testing.T) {
		tmpDir := t.TempDir()
		config, err := ParseDevContainerJSONWithSelector(tmpDir, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if config != nil {
			t.Error("expected nil config")
		}
	})
}
