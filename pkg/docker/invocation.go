package docker

import (
	"errors"
	"os"
	"runtime"
	"sort"
	"strings"
)

// envCaseInsensitive mirrors OS semantics; it is a variable so tests can exercise
// the Windows behaviour on any platform.
var envCaseInsensitive = runtime.GOOS == "windows"

// DockerInvocation is an immutable snapshot of how the docker CLI is invoked:
// the command and the fully merged environment (process env overlaid with the
// provider env). Capturing it once guarantees every consumer, including the
// in-process BuildKit client, targets the same daemon as the CLI paths.
type DockerInvocation struct {
	DockerCmd string            `json:"dockerCmd"`
	Env       map[string]string `json:"env"`
}

// NewDockerInvocation snapshots helper's command and environment. Provider
// entries override process entries of the same name.
func NewDockerInvocation(helper *DockerHelper) (*DockerInvocation, error) {
	if helper == nil || helper.DockerCommand == "" {
		return nil, errors.New("docker command is not configured")
	}
	if helper.Elevator != nil {
		return nil, errors.New("docker invocation does not support privilege elevation")
	}

	env := map[string]string{}
	for _, kv := range os.Environ() {
		setEnv(env, kv)
	}
	for _, kv := range helper.Environment {
		setEnv(env, kv)
	}
	return &DockerInvocation{DockerCmd: helper.DockerCommand, Env: env}, nil
}

// Lookup returns the value for name. Environment names are case-insensitive
// on Windows.
func (i *DockerInvocation) Lookup(name string) (string, bool) {
	if v, ok := i.Env[name]; ok {
		return v, true
	}
	if !envCaseInsensitive {
		return "", false
	}
	for k, v := range i.Env {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// Environ renders Env as a sorted KEY=VALUE slice suitable for exec.Cmd.Env.
func (i *DockerInvocation) Environ() []string {
	environ := make([]string, 0, len(i.Env))
	for k, v := range i.Env {
		environ = append(environ, k+"="+v)
	}
	sort.Strings(environ)
	return environ
}

func (i *DockerInvocation) validate() error {
	if i == nil || i.DockerCmd == "" {
		return errors.New("docker invocation has no docker command")
	}
	return nil
}

// setEnv stores a KEY=VALUE entry. Malformed entries and Windows drive-relative
// pseudo-variables (names starting with "=") are ignored.
func setEnv(env map[string]string, kv string) {
	idx := strings.IndexByte(kv, '=')
	if idx <= 0 {
		return
	}
	name, value := kv[:idx], kv[idx+1:]
	if envCaseInsensitive {
		for existing := range env {
			if strings.EqualFold(existing, name) {
				delete(env, existing)
			}
		}
	}
	env[name] = value
}
