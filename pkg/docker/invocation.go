package docker

import (
	"errors"
	"os"
	"runtime"
	"sort"
	"strings"
)

var envCaseInsensitive = runtime.GOOS == "windows"

// DockerInvocation is a source-independent snapshot of the docker command and
// the merged environment (process values overlaid by provider values). The
// environment is private: callers read it only through Lookup and Environ,
// which never expose the backing map. It is intentionally not serialized, so
// secret-bearing values cannot leak through encoding.
type DockerInvocation struct {
	DockerCmd string
	env       map[string]string
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
	return &DockerInvocation{DockerCmd: helper.DockerCommand, env: env}, nil
}

// Lookup returns the snapshot value for name. An explicitly empty value is
// distinct from an absent variable. Keys are case-insensitive on Windows only.
func (i *DockerInvocation) Lookup(name string) (string, bool) {
	if v, ok := i.env[name]; ok {
		return v, true
	}
	if !envCaseInsensitive {
		return "", false
	}
	for k, v := range i.env {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// Environ returns a fresh, sorted KEY=value slice; mutating it never affects
// the invocation.
func (i *DockerInvocation) Environ() []string {
	environ := make([]string, 0, len(i.env))
	for k, v := range i.env {
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

// daemonSelectorKeys are the environment variables that choose the daemon or
// its transport security; a provider override of any of them changes the
// effective Docker host.
var daemonSelectorKeys = []string{
	"DOCKER_HOST",
	"DOCKER_CONTEXT",
	"DOCKER_CONFIG",
	"DOCKER_TLS_VERIFY",
	"DOCKER_CERT_PATH",
}

// ProviderDaemonOverrides returns the daemon-selecting variable names whose
// provider-configured value differs from the process environment. An empty
// value and an absent variable are equivalent. Names only, never values.
func ProviderDaemonOverrides(helper *DockerHelper) []string {
	if helper == nil {
		return nil
	}
	provider := map[string]string{}
	for _, kv := range helper.Environment {
		setEnv(provider, kv)
	}
	inv := &DockerInvocation{env: provider}

	var overridden []string
	for _, key := range daemonSelectorKeys {
		want, ok := inv.Lookup(key)
		if !ok {
			continue
		}
		if want != os.Getenv(key) {
			overridden = append(overridden, key)
		}
	}
	return overridden
}
