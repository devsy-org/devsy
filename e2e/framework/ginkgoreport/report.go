// Package ginkgoreport validates Ginkgo JSON reports produced by E2E runs.
package ginkgoreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/onsi/ginkgo/v2/types"
)

// Check requires every spec carrying label to pass on its first attempt, exactly expected specs
// to be selected, and the report to prove a real, successful, unfocused run.
func Check(data []byte, label string, expected int) error {
	if label == "" || expected <= 0 {
		return errors.New("report validation needs a label and a positive expected-specs count")
	}
	report, err := readReport(data)
	if err != nil {
		return err
	}
	return checkSelectedSpecs(report.SpecReports, label, expected)
}

func readReport(data []byte) (types.Report, error) {
	var reports []types.Report
	if err := json.Unmarshal(data, &reports); err != nil {
		return types.Report{}, fmt.Errorf("decode Ginkgo report: %w", err)
	}
	if len(reports) != 1 {
		return types.Report{}, errors.New("expected exactly one Ginkgo suite report")
	}
	report := reports[0]
	if !report.SuiteSucceeded || report.SuiteConfig.DryRun || report.SuiteHasProgrammaticFocus {
		return types.Report{}, errors.New(
			"report must prove a successful run without dry-run or programmatic focus",
		)
	}
	return report, nil
}

func checkSelectedSpecs(specs types.SpecReports, label string, expected int) error {
	selected := 0
	for _, spec := range specs {
		if spec.LeafNodeType != types.NodeTypeIt || !slices.Contains(spec.Labels(), label) {
			continue
		}
		selected++
		if spec.State != types.SpecStatePassed || spec.NumAttempts != 1 {
			return fmt.Errorf("spec %q: state=%s attempts=%d, expected passed on the first attempt",
				spec.FullText(), spec.State, spec.NumAttempts)
		}
	}
	if selected != expected {
		return fmt.Errorf("label %q: expected %d specs, found %d", label, expected, selected)
	}
	return nil
}
