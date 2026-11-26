package up

import (
	"fmt"
	"sort"
	"strings"
)

type workspaceEnvAssignment struct {
	name  string
	value string
}

type resolvedEnvVar struct {
	assignment string
}

func parseWorkspaceEnvAssignment(raw string) (workspaceEnvAssignment, error) {
	name, value, ok := strings.Cut(raw, "=")
	if !ok || name == "" {
		return workspaceEnvAssignment{}, fmt.Errorf("invalid workspace environment assignment")
	}
	return workspaceEnvAssignment{name: name, value: value}, nil
}

func indexWorkspaceEnv(assignments []string) (map[string]workspaceEnvAssignment, error) {
	indexed := make(map[string]workspaceEnvAssignment, len(assignments))
	for _, raw := range assignments {
		assignment, err := parseWorkspaceEnvAssignment(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := indexed[assignment.name]; exists {
			return nil, fmt.Errorf(
				"workspace environment variable %q is assigned more than once",
				assignment.name,
			)
		}
		indexed[assignment.name] = assignment
	}
	return indexed, nil
}

func filterWorkspaceEnvRequests(
	base map[string]workspaceEnvAssignment,
	requests []envVarRequest,
) ([]envVarRequest, error) {
	filtered := make([]envVarRequest, 0, len(requests))
	for _, request := range requests {
		if _, exists := base[request.target]; !exists {
			filtered = append(filtered, request)
			continue
		}
		if request.origin == envVarAttached {
			continue
		}
		return nil, fmt.Errorf(
			"workspace environment target %q is set by both --workspace-env and --env; remove one explicit assignment",
			request.target,
		)
	}
	return filtered, nil
}

func composeWorkspaceEnv(
	base map[string]workspaceEnvAssignment,
	managed []resolvedEnvVar,
) ([]string, error) {
	for _, resolved := range managed {
		assignment, err := parseWorkspaceEnvAssignment(resolved.assignment)
		if err != nil {
			return nil, err
		}
		if _, exists := base[assignment.name]; exists {
			return nil, fmt.Errorf(
				"workspace environment variable %q is assigned more than once",
				assignment.name,
			)
		}
		base[assignment.name] = assignment
	}

	keys := make([]string, 0, len(base))
	for key := range base {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		assignment := base[key]
		result = append(result, assignment.name+"="+assignment.value)
	}
	return result, nil
}
