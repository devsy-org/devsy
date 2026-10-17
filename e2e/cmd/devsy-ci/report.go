package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/onsi/ginkgo/v2/types"
	"github.com/spf13/cobra"
)

const reportCommandName = "check-report"

func newReportCommand(deps *runenv.Dependencies) *cobra.Command {
	var path, label string
	var expected int
	cmd := &cobra.Command{
		Use:   reportCommandName,
		Short: "Require the selected Ginkgo specs to pass without skips or retries",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			data, err := deps.Files.ReadFile(path)
			if err != nil {
				return runenv.NewStageError(runenv.CodeGinkgoReportInvalid, reportCommandName,
					fmt.Errorf("read Ginkgo report: %w", err))
			}
			if err := checkReport(data, label, expected); err != nil {
				return runenv.NewStageError(runenv.CodeGinkgoReportInvalid, reportCommandName, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "report", "", "Ginkgo JSON report path")
	cmd.Flags().StringVar(&label, "label", "", "required spec label")
	cmd.Flags().IntVar(&expected, "expected-specs", 0, "required number of selected specs")
	return cmd
}

func checkReport(data []byte, label string, expected int) error {
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
	report, err := decodeSuiteReport(data)
	if err != nil {
		return types.Report{}, err
	}
	if !report.SuiteSucceeded || report.SuiteConfig.DryRun || report.SuiteHasProgrammaticFocus {
		return types.Report{}, errors.New(
			"report must prove a successful run without dry-run or programmatic focus",
		)
	}
	return report, nil
}

func decodeSuiteReport(data []byte) (types.Report, error) {
	var reports []types.Report
	if err := json.Unmarshal(data, &reports); err != nil {
		return types.Report{}, fmt.Errorf("decode Ginkgo report: %w", err)
	}
	if len(reports) != 1 {
		return types.Report{}, errors.New("expected exactly one Ginkgo suite report")
	}
	return reports[0], nil
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
