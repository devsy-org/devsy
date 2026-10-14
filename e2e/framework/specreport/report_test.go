package specreport

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// wslSpecs are the spec titles the docker-wsl-ssh profile requires of the fixtures.
var wslSpecs = []string{
	"dials the provider's daemon through docker system dial-stdio",
	"loads an image built by raw BuildKit into the provider's daemon",
	"BuildDevContainer builds on the provider's SSH daemon despite a dead process DOCKER_HOST",
	"BuildDevContainer fails instead of falling back to the process daemon",
}

var wslWant = Expectation{Label: fixtureLabel, Required: wslSpecs}

type ReportSuite struct{ suite.Suite }

func TestReportSuite(t *testing.T) { suite.Run(t, new(ReportSuite)) }

func (s *ReportSuite) TestFixtureMatrix() {
	for _, tc := range []struct {
		fixture string
		want    Expectation
		errPart string
	}{
		{validFixture, wslWant, ""},
		{"valid_5of5.json", wslWant, ""},
		{"large_skipped_403.json", wslWant, ""},
		{"zero_executed.json", wslWant, noExecutedMessage},
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
		{validFixture, Expectation{Label: "other"}, noExecutedMessage},
		{validFixture, Expectation{Label: "DOCKER-INTERNAL-BUILDKIT"}, ""},
	} {
		s.Run(tc.fixture+"/"+tc.errPart, func() {
			_, err := Validate(loadFixture(s.T(), tc.fixture), tc.want)
			if tc.errPart == "" {
				s.Require().NoError(err)
				return
			}
			s.Require().ErrorContains(err, tc.errPart)
		})
	}
}

func (s *ReportSuite) TestLargeSuiteCounts() {
	sum, err := Validate(loadFixture(s.T(), "large_skipped_403.json"), wslWant)
	s.Require().NoError(err)
	s.Equal(407, sum.Total)
	s.Equal(4, sum.Executed)
	s.Equal(4, sum.Passed)
	s.Equal(403, sum.SkippedUnmatched)
}

// The real artifact was produced by ginkgo/v2 v2.32.1 and proves the field
// names and string-valued enums the parser relies on.
func (s *ReportSuite) TestRealGinkgoArtifact() {
	data := loadFixture(s.T(), "real_ginkgo_report.json")

	sum, err := Validate(data, Expectation{Label: fixtureLabel})
	s.Require().NoError(err)
	s.Equal(2, sum.Executed)
	s.Equal(2, sum.Passed)
	s.Equal(1, sum.SkippedSelected)
	s.Equal(2, sum.SkippedUnmatched)

	_, err = Validate(data, wslWant)
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
		"empty spec array": {`[{"SpecReports":[]}]`, noExecutedMessage},
		"not an array":     {`{"SpecReports":[]}`, "not a Ginkgo JSON report array"},
	} {
		s.Run(name, func() {
			_, err := Validate([]byte(tc.json), Expectation{Label: "x"})
			s.Require().ErrorContains(err, tc.errPart)
		})
	}
}

func (s *ReportSuite) TestLabelFromLeafNode() {
	data := `[{"SpecReports":[{"LeafNodeType":"It","LeafNodeText":"a","State":"passed",` +
		`"LeafNodeLabels":["x"],"ContainerHierarchyLabels":[]}]}]`
	_, err := Validate([]byte(data), Expectation{Label: "x"})
	s.Require().NoError(err)
}

func (s *ReportSuite) TestSummaryFormat() {
	sum, err := Validate(loadFixture(s.T(), validFixture), wslWant)
	s.Require().NoError(err)
	text := FormatSummary("/r/report.json", sum)
	s.Contains(text, "/r/report.json: 4 specs, 4 executed (4 passed)")
	s.Contains(text, `passed      "dials the provider's daemon through docker system dial-stdio"`)
}
