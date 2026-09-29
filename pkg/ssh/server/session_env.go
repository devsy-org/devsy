package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/devsy-org/devsy/pkg/secrets"
)

func mergeSessionEnvironment(base, overrides, defaults []string) []string {
	values := make(map[string]string, len(base)+len(overrides)+len(defaults))
	order := make([]string, 0, len(base)+len(overrides)+len(defaults))
	for _, assignment := range base {
		setEnvironmentAssignment(values, &order, assignment, false)
	}
	for _, assignment := range overrides {
		setEnvironmentAssignment(values, &order, assignment, true)
	}
	for _, assignment := range defaults {
		setEnvironmentAssignment(values, &order, assignment, false)
	}
	env := make([]string, 0, len(order))
	for _, name := range order {
		env = append(env, name+"="+values[name])
	}
	return env
}

func setEnvironmentAssignment(
	values map[string]string,
	order *[]string,
	assignment string,
	replace bool,
) {
	name, value, ok := strings.Cut(assignment, "=")
	if !ok || name == "" {
		return
	}
	if _, exists := values[name]; exists {
		if replace {
			values[name] = value
		}
		return
	}
	*order = append(*order, name)
	values[name] = value
}

func readSessionSecretEnvironment(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	env := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || secrets.ValidateName(entry.Name()) != nil {
			continue
		}
		// #nosec G304 -- fixed secret directory and validated name.
		value, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf(
				"read session secret environment variable %s: %w",
				entry.Name(),
				err,
			)
		}
		env = append(env, entry.Name()+"="+string(value))
	}
	return env, nil
}
