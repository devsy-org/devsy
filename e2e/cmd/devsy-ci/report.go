package main

import (
	"fmt"

	"github.com/devsy-org/devsy/e2e/framework/ginkgoreport"
	"github.com/devsy-org/devsy/e2e/framework/runenv"
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
			if err := ginkgoreport.Check(data, label, expected); err != nil {
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
