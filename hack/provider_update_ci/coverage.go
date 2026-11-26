package main

import (
	"fmt"
	"sort"
)

type testState struct {
	started bool
	passed  bool
	skipped bool
	failed  bool
}

func validateTestCoverage(expected map[testID]struct{}, events map[testID]testState) []string {
	if len(expected) == 0 {
		return []string{"go test produced no discoverable test roots"}
	}

	var issues []string
	for id := range expected {
		if _, ok := events[id]; !ok {
			issues = append(
				issues,
				fmt.Sprintf(
					"discovered test produced no JSON events: %s (%s)",
					id.name,
					id.packagePath,
				),
			)
		}
	}

	for id, state := range events {
		issues = append(issues, observedTestIssues(id, state)...)
	}

	sort.Strings(issues)
	return issues
}

func observedTestIssues(id testID, state testState) []string {
	var issues []string
	if state.skipped {
		issues = append(
			issues,
			fmt.Sprintf("observed test was skipped: %s (%s)", id.name, id.packagePath),
		)
	}
	if state.failed {
		issues = append(
			issues,
			fmt.Sprintf("observed test failed: %s (%s)", id.name, id.packagePath),
		)
	}
	if !state.started {
		issues = append(
			issues,
			fmt.Sprintf("observed test did not start: %s (%s)", id.name, id.packagePath),
		)
	}
	if !state.passed {
		issues = append(
			issues,
			fmt.Sprintf("observed test did not pass: %s (%s)", id.name, id.packagePath),
		)
	}
	return issues
}
