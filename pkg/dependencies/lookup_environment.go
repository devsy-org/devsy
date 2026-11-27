package dependencies

import (
	"errors"
	"maps"
	"runtime"
	"strings"
)

// LookupEnvironment is an immutable, already-effective invocation snapshot.
// Its zero value is invalid. It never fills absent values from the process.
type LookupEnvironment struct {
	values  map[string]string
	cwd     string
	windows bool
}

func NewLookupEnvironment(values map[string]string, cwd string) (LookupEnvironment, error) {
	return newLookupEnvironment(values, cwd, runtime.GOOS == lookupWindows)
}

func newLookupEnvironment(
	values map[string]string,
	cwd string,
	windows bool,
) (LookupEnvironment, error) {
	environment := LookupEnvironment{values: maps.Clone(values), cwd: cwd, windows: windows}
	if !environment.localAbsolute(cwd) {
		return LookupEnvironment{}, errors.New(
			"lookup working directory must be a local absolute path",
		)
	}
	if !windows {
		return environment, nil
	}
	environment.values = make(map[string]string, len(values))
	for key, value := range values {
		key = strings.ToUpper(key)
		if _, exists := environment.values[key]; exists {
			return LookupEnvironment{}, errors.New("ambiguous Windows environment key")
		}
		environment.values[key] = value
	}
	return environment, nil
}

func (e LookupEnvironment) Value(key string) (string, bool) {
	if e.windows {
		key = strings.ToUpper(key)
	}
	value, present := e.values[key]
	return value, present
}

func (e LookupEnvironment) WorkingDirectory() string { return e.cwd }

// absolute preserves symlink and .. traversal semantics rather than cleaning
// them away. It refuses an empty path, a zero-value environment, and every form
// whose meaning depends on ambient drive state or names a remote or device path.
func (e LookupEnvironment) absolute(path string) (string, bool) {
	if e.cwd == "" || path == "" || strings.ContainsRune(path, 0) {
		return "", false
	}
	if e.localAbsolute(path) {
		return path, true
	}
	if e.rootedOrQualified(path) {
		return "", false
	}
	separator := e.separator()
	joined := strings.TrimRight(e.cwd, separator) + separator + path
	if e.windows && !e.localAbsolute(joined) {
		return "", false
	}
	return joined, true
}
