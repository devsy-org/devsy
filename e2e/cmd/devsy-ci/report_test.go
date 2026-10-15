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

func (s *ReportSuite) TestRequiresEverySelectedSpecToPassOnFirstAttempt() {
	for _, tc := range []struct {
		name   string
		mutate func(*types.Report)
	}{
		{"failed suite", func(r *types.Report) { r.SuiteSucceeded = false }},
		{"dry run", func(r *types.Report) { r.SuiteConfig.DryRun = true }},
		{"focused suite", func(r *types.Report) { r.SuiteHasProgrammaticFocus = true }},
		{"skipped selected spec", func(r *types.Report) { r.SpecReports[0].State = types.SpecStateSkipped }},
		{"pending selected spec", func(r *types.Report) { r.SpecReports[0].State = types.SpecStatePending }},
		{"failed selected spec", func(r *types.Report) { r.SpecReports[0].State = types.SpecStateFailed }},
		{"retried spec", func(r *types.Report) { r.SpecReports[0].NumAttempts = 2 }},
		{"not executed", func(r *types.Report) { r.SpecReports[0].NumAttempts = 0 }},
		{"wrong label", func(r *types.Report) { r.SpecReports[0].ContainerHierarchyLabels = nil }},
		{"extra selected spec", func(r *types.Report) { r.SpecReports = append(r.SpecReports, r.SpecReports[0]) }},
	} {
		s.Run(tc.name, func() {
			r := passedReport()
			tc.mutate(&r)
			data, err := json.Marshal([]types.Report{r})
			s.Require().NoError(err)
			s.Error(checkReport(data, reportLabel, 1))
		})
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

func (s *ReportSuite) TestRejectsMissingOrMalformedReportsAndInvalidExpectations() {
	for _, data := range []string{"not JSON", "null", "[]", "[{},{}]", "[{}]"} {
		s.Error(checkReport([]byte(data), reportLabel, 1), data)
	}
	s.Error(checkReport(nil, "", 1))
	s.Error(checkReport(nil, reportLabel, 0))
	s.Error(checkReport(nil, reportLabel, -1))
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
