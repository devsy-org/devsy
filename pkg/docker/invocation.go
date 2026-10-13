package docker

import (
	"errors"
	"os"
	"runtime"
	"sort"
	"strings"
)

var envCaseInsensitive = runtime.GOOS == "windows"

type DockerInvocation struct {
	DockerCmd string            `json:"dockerCmd"`
	Env       map[string]string `json:"env"`
}

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
