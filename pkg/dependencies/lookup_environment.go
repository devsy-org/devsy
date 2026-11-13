package dependencies

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"runtime"
	"strings"
)

// LookupEnvironment is an immutable, already-effective invocation snapshot.
// Its zero value is invalid. It never fills absent values from the process.
type LookupEnvironment struct {
	values  map[string]string
	cwd     string
	inspect func(context.Context, string) (lookupFile, error)
}

func NewLookupEnvironment(values map[string]string, cwd string) (LookupEnvironment, error) {
	if !filepath.IsAbs(cwd) || strings.ContainsRune(cwd, 0) {
		return LookupEnvironment{}, errors.New("lookup working directory must be absolute")
	}
	copied := maps.Clone(values)
	if runtime.GOOS == lookupWindows {
		copied = make(map[string]string, len(values))
		for key, value := range values {
			key = strings.ToUpper(key)
			if _, exists := copied[key]; exists {
				return LookupEnvironment{}, errors.New("ambiguous Windows environment key")
			}
			copied[key] = value
		}
	}
	return LookupEnvironment{values: copied, cwd: cwd}, nil
}

func (e LookupEnvironment) Value(key string) (string, bool) {
	if runtime.GOOS == lookupWindows {
		key = strings.ToUpper(key)
	}
	value, present := e.values[key]
	return value, present
}

func (e LookupEnvironment) WorkingDirectory() string { return e.cwd }

// absolute preserves symlink/.. traversal semantics rather than cleaning it away.
func (e LookupEnvironment) absolute(path string) (string, bool) {
	if filepath.IsAbs(path) {
		return path, true
	}
	// Drive-relative and root-relative Windows paths depend on ambient drive state.
	if filepath.VolumeName(path) != "" ||
		(runtime.GOOS == lookupWindows && strings.HasPrefix(path, `\`)) {
		return "", false
	}
	if runtime.GOOS == lookupWindows && strings.HasPrefix(path, "/") {
		return "", false
	}
	return e.cwd + string(filepath.Separator) + path, true
}

func (e LookupEnvironment) inspectFile(ctx context.Context, path string) (lookupFile, error) {
	if err := ctx.Err(); err != nil {
		return lookupFile{}, err
	}
	inspect := e.inspect
	if inspect == nil {
		inspect = inspectLookupFile
	}
	observed, err := inspect(ctx, path)
	if canceled := ctx.Err(); canceled != nil {
		return lookupFile{}, canceled
	}
	return observed, err
}
