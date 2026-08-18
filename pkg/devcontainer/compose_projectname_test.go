package devcontainer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

const (
	testComposeVersion = "2.30.0"
	testRunnerID       = "random-runner-id"
	testEnvName        = "env-name"
	testYamlName       = "yaml-name"
)

// newResolveRunner builds a runner wired to a temp workspace with the devcontainer
// config rooted at <workspace>/.devcontainer/devcontainer.json.
func newResolveRunner(t *testing.T) (*runner, string) {
	t.Helper()
	ws := t.TempDir()
	configDir := filepath.Join(ws, ".devcontainer")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	r := &runner{
		localWorkspaceFolder: ws,
		id:                   testRunnerID,
	}
	return r, configDir
}

func writeComposeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeFile(t, path, body)
	return path
}

// resolveProject loads a compose-go project the same way runDockerCompose
// does, then resolves its project name.
func resolveProject(t *testing.T, r *runner, configDir, composeFile string) (string, error) {
	t.Helper()
	helper := &compose.ComposeHelper{Version: testComposeVersion}
	projFiles := composeProjectFiles{composeFiles: []string{composeFile}}
	if envFile := filepath.Join(configDir, ".env"); fileExists(envFile) {
		projFiles.envFiles = []string{envFile}
	}
	project, err := compose.LoadDockerComposeProject(
		t.Context(), projFiles.composeFiles, projFiles.envFiles,
	)
	if err != nil {
		return "", err
	}
	return r.resolveComposeProjectName(helper, project, projFiles)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// unsetComposeProjectNameEnv ensures COMPOSE_PROJECT_NAME is absent from the
// environment (not merely empty: compose-go treats a shell variable that is
// present-but-empty differently from one that was never exported).
func unsetComposeProjectNameEnv(t *testing.T) {
	t.Helper()
	prev, wasSet := os.LookupEnv(compose.ComposeProjectNameEnv)
	if err := os.Unsetenv(compose.ComposeProjectNameEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	t.Cleanup(func() {
		if wasSet {
			if err := os.Setenv(compose.ComposeProjectNameEnv, prev); err != nil {
				t.Fatalf("restore env: %v", err)
			}
		}
	})
}

func TestResolveComposeProjectName(t *testing.T) {
	cases := []struct {
		name     string
		envName  string
		envBody  string
		yamlBody string
		want     string
	}{
		{
			name:     "shell env overrides everything",
			envName:  "shell-name",
			want:     "shell-name",
			yamlBody: "name: " + testYamlName + "\nservices: {}\n",
			envBody:  "COMPOSE_PROJECT_NAME=" + testEnvName + "\n",
		},
		{
			name:     "env file overrides yaml name",
			want:     testEnvName,
			envBody:  "COMPOSE_PROJECT_NAME=" + testEnvName + "\n",
			yamlBody: "name: " + testYamlName + "\nservices: {}\n",
		},
		{
			name:     "yaml name used when no env",
			want:     testYamlName,
			yamlBody: "name: " + testYamlName + "\nservices: {}\n",
		},
		{
			name:     "interpolated yaml name is resolved, not read raw",
			want:     testYamlName,
			yamlBody: "name: ${UNSET_NAME_VAR:-" + testYamlName + "}\nservices: {}\n",
		},
		{
			name:     "falls back to sanitized runner id when no source",
			want:     testRunnerID,
			yamlBody: "services: {}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envName != "" {
				t.Setenv(compose.ComposeProjectNameEnv, tc.envName)
			} else {
				unsetComposeProjectNameEnv(t)
			}
			r, configDir := newResolveRunner(t)
			composeFile := writeComposeFile(t, configDir, "compose.yml", tc.yamlBody)
			if tc.envBody != "" {
				writeComposeFile(t, configDir, ".env", tc.envBody)
			}
			got, err := resolveProject(t, r, configDir, composeFile)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveComposeProjectName_ShellEnvSetEmptyBlocksEnvFile(t *testing.T) {
	t.Setenv(compose.ComposeProjectNameEnv, "")
	r, configDir := newResolveRunner(t)
	composeFile := writeComposeFile(
		t, configDir, "compose.yml", "name: "+testYamlName+"\nservices: {}\n",
	)
	writeComposeFile(t, configDir, ".env", "COMPOSE_PROJECT_NAME="+testEnvName+"\n")

	got, err := resolveProject(t, r, configDir, composeFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != testYamlName {
		t.Errorf("got %q, want %q", got, testYamlName)
	}
}

func TestResolveComposeProjectName_MultiFileLastNameWins(t *testing.T) {
	unsetComposeProjectNameEnv(t)
	r, configDir := newResolveRunner(t)
	base := writeComposeFile(t, configDir, "base.yml", "name: base-app\nservices: {}\n")
	over := writeComposeFile(t, configDir, "override.yml", "name: override-app\nservices: {}\n")

	helper := &compose.ComposeHelper{Version: testComposeVersion}
	projFiles := composeProjectFiles{composeFiles: []string{base, over}}
	project, err := compose.LoadDockerComposeProject(t.Context(), projFiles.composeFiles, nil)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	got, err := r.resolveComposeProjectName(helper, project, projFiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "override-app" {
		t.Errorf("got %q, want %q", got, "override-app")
	}
}

func TestBaseEnvFiles(t *testing.T) {
	t.Run("collects config dir and workspace root .env", func(t *testing.T) {
		r, configDir := newResolveRunner(t)
		writeComposeFile(t, configDir, ".env", "COMPOSE_PROJECT_NAME=dc\n")
		writeComposeFile(t, r.localWorkspaceFolder, ".env", "OTHER=1\n")
		parsed := &config.SubstitutedConfig{Config: &config.DevContainerConfig{}}
		parsed.Config.Origin = filepath.Join(configDir, "devcontainer.json")

		got := r.baseEnvFiles(parsed)
		if len(got) != 2 {
			t.Fatalf("got %d env files, want 2: %v", len(got), got)
		}
		if got[0] != filepath.Join(configDir, ".env") {
			t.Errorf("first = %q, want config dir .env", got[0])
		}
		if got[1] != filepath.Join(r.localWorkspaceFolder, ".env") {
			t.Errorf("second = %q, want workspace root .env", got[1])
		}
	})

	t.Run("deduplicates when config dir is the workspace root", func(t *testing.T) {
		ws := t.TempDir()
		r := &runner{localWorkspaceFolder: ws, id: testRunnerID}
		writeComposeFile(t, ws, ".env", "OTHER=1\n")
		parsed := &config.SubstitutedConfig{Config: &config.DevContainerConfig{}}
		parsed.Config.Origin = filepath.Join(ws, ".devcontainer.json")

		got := r.baseEnvFiles(parsed)
		if len(got) != 1 {
			t.Fatalf("got %d env files, want 1 (deduped): %v", len(got), got)
		}
	})

	t.Run("skips missing files", func(t *testing.T) {
		r, configDir := newResolveRunner(t)
		parsed := &config.SubstitutedConfig{Config: &config.DevContainerConfig{}}
		parsed.Config.Origin = filepath.Join(configDir, "devcontainer.json")

		if got := r.baseEnvFiles(parsed); len(got) != 0 {
			t.Fatalf("got %d env files, want 0: %v", len(got), got)
		}
	})
}

func TestComposeDirEnvFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "dockerfiles")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	composeA := writeComposeFile(t, sub, "docker-compose.yml", "services: {}\n")
	composeB := writeComposeFile(t, dir, "compose.dev.yml", "services: {}\n")
	writeComposeFile(t, sub, ".env", "COMPOSE_PROJECT_NAME=x\n")
	writeComposeFile(t, dir, ".env", "COMPOSE_PROJECT_NAME=y\n")

	got := composeDirEnvFiles([]string{composeA, composeB})
	if len(got) != 2 {
		t.Fatalf("got %d env files, want 2: %v", len(got), got)
	}
	if got[0] != filepath.Join(sub, ".env") {
		t.Errorf("first = %q, want dockerfiles/.env", got[0])
	}
	if got[1] != filepath.Join(dir, ".env") {
		t.Errorf("second = %q, want root .env", got[1])
	}
}

func TestAppendEnvFilesDeduplicates(t *testing.T) {
	const envA, envB, envC = "/a.env", "/b.env", "/c.env"
	base := []string{envA, envB}
	additional := []string{envB, envC, envA}
	got := appendEnvFiles(base, additional)
	want := []string{envA, envB, envC}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("index %d = %q, want %q", i, got[i], v)
		}
	}
}
