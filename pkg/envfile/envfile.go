package envfile

import (
	"encoding/json"
	"maps"
	"os"

	"github.com/devsy-org/devsy/pkg/log"
)

var location = "/etc/envfile.json"

type EnvFile struct {
	// Env holds the environment variables to set
	Env map[string]string `json:"env,omitempty"`
}

func Apply() {
	envFile, ok := readEnvFile()
	if !ok {
		return
	}

	for k, v := range envFile.Env {
		_ = os.Setenv(k, v)
	}
}

func readEnvFile() (EnvFile, bool) {
	out, err := os.ReadFile(location)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Debugf("Error reading envfile: %v", err)
			return EnvFile{}, false
		}
		return EnvFile{}, true
	}

	var envFile EnvFile
	if err := json.Unmarshal(out, &envFile); err != nil {
		log.Debugf("Error parsing envfile: %v", err)
		return EnvFile{}, false
	}
	return envFile, true
}

func MergeAndApply(env map[string]string) {
	if len(env) == 0 {
		return
	}

	envFile, ok := readEnvFile()
	if !ok {
		return
	}

	if envFile.Env == nil {
		envFile.Env = map[string]string{}
	}
	maps.Copy(envFile.Env, env)

	out, err := json.Marshal(envFile)
	if err != nil {
		log.Debugf("Error marshalling envfile: %v", err)
		return
	}

	// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
	if err := os.WriteFile(location, out, 0o666); err != nil {
		log.Debugf("Error writing envfile: %v", err)
		return
	}

	for k, v := range envFile.Env {
		_ = os.Setenv(k, v)
	}
}
