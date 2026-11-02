package main

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/onsi/ginkgo/v2/types"
	"github.com/stretchr/testify/suite"
)

const reportLabel = "docker-internal-buildkit"

type ReportSuite struct{ suite.Suite }

func TestReportSuite(t *testing.T) { suite.Run(t, new(ReportSuite)) }

func passedReport() types.Report {
	return types.Report{
		SuiteSucceeded: true,
		SpecReports: types.SpecReports{
			{
				LeafNodeType: types.NodeTypeIt, LeafNodeText: "builds on the selected daemon",
				ContainerHierarchyLabels: [][]string{{reportLabel}},
				State:                    types.SpecStatePassed, NumAttempts: 1,
			},
			{
				LeafNodeType: types.NodeTypeIt, LeafNodeText: "unrelated spec",
				LeafNodeLabels: []string{"other-label"}, State: types.SpecStateSkipped,
			},
		},
	}
}

func (s *ReportSuite) TestReportCommandChecksTheRequestedFile() {
	data, err := json.Marshal([]types.Report{passedReport()})
	s.Require().NoError(err)
	rig := runenvtest.NewRig(runenv.GoosLinux, nil)
	s.Require().NoError(rig.Files.WriteFile("report.json", data, 0o600))
	cmd := newRootCommand(rig.Dependencies)
	cmd.SetArgs(
		[]string{
			reportCommandName,
			"--report",
			"report.json",
			"--label",
			reportLabel,
			"--expected-specs",
			"1",
		},
	)
	s.NoError(cmd.Execute())
}

func (s *ReportSuite) TestRejectsMissingReportFile() {
	cmd := newRootCommand(runenvtest.NewRig(runenv.GoosLinux, nil).Dependencies)
	cmd.SetArgs(
		[]string{
			reportCommandName,
			"--report",
			"does-not-exist.json",
			"--label",
			reportLabel,
			"--expected-specs",
			"1",
		},
	)
	err := cmd.Execute()
	s.ErrorContains(err, "read Ginkgo report")
	s.Equal(runenv.ExitCodeFailure, runenv.ExitCodeForError(err))
}
