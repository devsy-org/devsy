package main

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

var wslWant = reportExpectation{Label: buildkitLabel, Required: dockerWSLRequiredSpecs}

type ReportSuite struct{ suite.Suite }

func TestReportSuite(t *testing.T) { suite.Run(t, new(ReportSuite)) }

func (s *ReportSuite) TestFixtureMatrix() {
	for _, tc := range []struct {
		fixture string
		want    reportExpectation
		errPart string
	}{
		{fixtureValid4, wslWant, ""},
		{"valid_5of5.json", wslWant, ""},
		{"large_skipped_403.json", wslWant, ""},
		{"zero_executed.json", wslWant, msgNoExecuted},
		{"missing_required.json", wslWant, "required spec missing"},
		{"duplicate_required.json", wslWant, "required spec duplicated"},
		{"required_skipped.json", wslWant, "required spec skipped"},
		{"failed.json", wslWant, "failed"},
		{"timedout.json", wslWant, "timedout"},
		{"panicked.json", wslWant, "panicked"},
		{"malformed.json", wslWant, "not a Ginkgo JSON report"},
		{"empty.json", wslWant, "report file is empty"},
		{"multiple_reports.json", wslWant, "exactly one suite report, got 2"},
		{"wrong_label.json", wslWant, "executed without label"},
		{"missing_spec_reports.json", wslWant, "no SpecReports"},
		{fixtureValid4, reportExpectation{Label: "other"}, msgNoExecuted},
		{fixtureValid4, reportExpectation{Label: "DOCKER-INTERNAL-BUILDKIT"}, ""},
	} {
		s.Run(tc.fixture+"/"+tc.errPart, func() {
			_, err := validateReport(readFixture(s.T(), tc.fixture), tc.want)
			if tc.errPart == "" {
				s.Require().NoError(err)
				return
			}
			s.Require().ErrorContains(err, tc.errPart)
		})
	}
}

func (s *ReportSuite) TestLargeSuiteCounts() {
	sum, err := validateReport(readFixture(s.T(), "large_skipped_403.json"), wslWant)
	s.Require().NoError(err)
	s.Equal(407, sum.Total)
	s.Equal(4, sum.Executed)
	s.Equal(4, sum.Passed)
	s.Equal(403, sum.SkippedUnmatched)
}

// The real artifact was produced by ginkgo/v2 v2.32.1 and proves the field
// names and string-valued enums the parser relies on.
func (s *ReportSuite) TestRealGinkgoArtifact() {
	data := readFixture(s.T(), "real_ginkgo_report.json")

	sum, err := validateReport(data, reportExpectation{Label: buildkitLabel})
	s.Require().NoError(err)
	s.Equal(2, sum.Executed)
	s.Equal(2, sum.Passed)
	s.Equal(1, sum.SkippedSelected)
	s.Equal(2, sum.SkippedUnmatched)

	_, err = validateReport(data, wslWant)
	s.Require().ErrorContains(err, "required spec missing")
}

func (s *ReportSuite) TestAmbiguousAndSuiteFailures() {
	for name, tc := range map[string]struct {
		json    string
		errPart string
	}{
		"unknown state": {
			`[{"SpecReports":[{"LeafNodeType":"It","LeafNodeText":"a","State":"weird",` +
				`"ContainerHierarchyLabels":[["x"]]}]}]`,
			"ambiguous state",
		},
		"null state": {
			`[{"SpecReports":[{"LeafNodeType":"It","LeafNodeText":"a","State":null}]}]`,
			"lacks LeafNodeType or State",
		},
		"suite not succeeded": {
			`[{"SuiteSucceeded":false,"SpecReports":[{"LeafNodeType":"It","LeafNodeText":"a",` +
				`"State":"passed","ContainerHierarchyLabels":[["x"]]}]}]`,
			"SuiteSucceeded=false",
		},
		"failed BeforeSuite": {
			`[{"SpecReports":[{"LeafNodeType":"BeforeSuite","LeafNodeText":"","State":"failed"},` +
				`{"LeafNodeType":"It","LeafNodeText":"a","State":"passed",` +
				`"ContainerHierarchyLabels":[["x"]]}]}]`,
			"BeforeSuite",
		},
		"special failure reason": {
			`[{"SpecialSuiteFailureReasons":["Interrupted by user"],"SpecReports":[{` +
				`"LeafNodeType":"It","LeafNodeText":"a","State":"passed",` +
				`"ContainerHierarchyLabels":[["x"]]}]}]`,
			"Interrupted by user",
		},
		"trailing data":    {`[{"SpecReports":[]}] []`, "trailing data"},
		"empty spec array": {`[{"SpecReports":[]}]`, msgNoExecuted},
		"not an array":     {`{"SpecReports":[]}`, "not a Ginkgo JSON report array"},
	} {
		s.Run(name, func() {
			_, err := validateReport([]byte(tc.json), reportExpectation{Label: "x"})
			s.Require().ErrorContains(err, tc.errPart)
		})
	}
}

func (s *ReportSuite) TestLabelFromLeafNode() {
	data := `[{"SpecReports":[{"LeafNodeType":"It","LeafNodeText":"a","State":"passed",` +
		`"LeafNodeLabels":["x"],"ContainerHierarchyLabels":[]}]}]`
	_, err := validateReport([]byte(data), reportExpectation{Label: "x"})
	s.Require().NoError(err)
}

func (s *ReportSuite) TestSummaryFormat() {
	sum, err := validateReport(readFixture(s.T(), fixtureValid4), wslWant)
	s.Require().NoError(err)
	text := formatSummary("/r/report.json", sum)
	s.Contains(text, "/r/report.json: 4 specs, 4 executed (4 passed)")
	s.Contains(text, `passed      "dials the provider's daemon through docker system dial-stdio"`)
}
