package ginkgoreport

import (
	"encoding/json"
	"testing"

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
			s.Error(Check(data, reportLabel, 1))
		})
	}
}

func (s *ReportSuite) TestRejectsMissingOrMalformedReportsAndInvalidExpectations() {
	for _, data := range []string{"not JSON", "null", "[]", "[{},{}]", "[{}]"} {
		s.Error(Check([]byte(data), reportLabel, 1), data)
	}
	s.Error(Check(nil, "", 1))
	s.Error(Check(nil, reportLabel, 0))
	s.Error(Check(nil, reportLabel, -1))
}
