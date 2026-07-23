package options

import (
	"maps"
	"os"
	"slices"
	"strings"
)

// GitIdentityEnvVars are the git author/committer environment variables
// inherited from the host so commits made inside the workspace carry the
// user's identity.
var GitIdentityEnvVars = []string{
	"GIT_AUTHOR_NAME",
	"GIT_AUTHOR_EMAIL",
	"GIT_AUTHOR_DATE",
	"GIT_COMMITTER_NAME",
	"GIT_COMMITTER_EMAIL",
	"GIT_COMMITTER_DATE",
}

// Takes a list of assignments in KEY=VALUE format, a map of option to propagate, and an environment variable prefix,
// and returns a new list with additional assignments from environment variables for any options not already assigned.
func InheritOptionsFromEnvironment[Map ~map[string]V, V any](
	assignments []string,
	options Map,
	prefix string,
) []string {
	return InheritFromEnvironment(
		assignments,
		slices.Collect(maps.Keys(options)),
		strings.ToUpper(prefix),
	)
}

// Takes a list of assignments in KEY=VALUE format, a list of option names to check, and an environment variable prefix,
// and returns a new list with additional assignments from environment variables for any names not already assigned.
func InheritFromEnvironment(
	assignments []string,
	names []string,
	prefix string,
) []string {
	assigned := assignedNames(assignments)

	result := assignments
	for _, name := range names {
		if assigned[name] {
			continue
		}
		if value, exists := os.LookupEnv(prefix + name); exists {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func assignedNames(assignments []string) map[string]bool {
	names := make(map[string]bool, len(assignments))
	for _, assignment := range assignments {
		if before, _, ok := strings.Cut(assignment, "="); ok {
			names[before] = true
		}
	}
	return names
}
