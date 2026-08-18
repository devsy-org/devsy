package compose

import (
	"os"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// ComposeProjectNameEnv is the environment/file variable Docker Compose uses
// to override the project name: https://docs.docker.com/compose/how-tos/project-name/
const ComposeProjectNameEnv = "COMPOSE_PROJECT_NAME"

// ProjectNameFromEnvFiles returns the first non-empty COMPOSE_PROJECT_NAME
// found scanning envFiles in order. Missing files are skipped.
func ProjectNameFromEnvFiles(envFiles []string) (string, error) {
	for _, envFile := range envFiles {
		env, err := godotenv.Read(envFile)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if name := env[ComposeProjectNameEnv]; name != "" {
			return name, nil
		}
	}
	return "", nil
}

// composeNameFile is the subset of a compose file relevant to name detection.
type composeNameFile struct {
	Name string `yaml:"name"`
}

// ComposeFilesDeclareName reports whether any compose file declares a
// top-level "name" (mirroring compose-go's own isNamed check). Presence is
// all that's needed: the caller uses the already-loaded project's
// interpolated Name when this is true.
func ComposeFilesDeclareName(composeFiles []string) (bool, error) {
	for _, file := range composeFiles {
		// #nosec G304 -- file paths are resolved from the trusted devcontainer config.
		b, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		var parsed composeNameFile
		if err := yaml.Unmarshal(b, &parsed); err != nil {
			continue
		}
		if parsed.Name != "" {
			return true, nil
		}
	}
	return false, nil
}
