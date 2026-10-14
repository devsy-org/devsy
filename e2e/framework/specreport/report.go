package specreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

const MaxReportBytes = 64 << 20

// Ginkgo v2 serializes SpecState and NodeType as strings (see
// types.ssEnumSupport / ntEnumSupport in ginkgo/v2 v2.32.1). The state for a
// timed-out spec is "timedout".
const (
	nodeTypeIt = "It"

	stateSkipped = "skipped"
	statePending = "pending"
	statePassed  = "passed"
)

var failureStates = []string{"failed", "aborted", "panicked", "interrupted", "timedout"}

// ginkgoReport is the subset of ginkgo/v2 types.Report this tool needs. Unknown
// fields are ignored; SpecReports is nil when absent or null.
type ginkgoReport struct {
	SuiteSucceeded             *bool
	SpecialSuiteFailureReasons []string
	SpecReports                []ginkgoSpec
}

// ginkgoSpec is the subset of types.SpecReport this tool needs. FullText is not
// serialized by Ginkgo, so identity is the leaf node text.
type ginkgoSpec struct {
	ContainerHierarchyLabels [][]string
	LeafNodeType             string
	LeafNodeLabels           []string
	LeafNodeText             string
	State                    string
}

// reportExpectation is what a successful report must contain.
type Expectation struct {
	Label    string
	Required []string // spec identities (leaf text) that must each run once and pass
}

type SpecLine struct {
	Text  string
	State string
}

// ReportSummary counts specs and lists those selected by the label.
type ReportSummary struct {
	Total            int
	Executed         int
	Passed           int
	SkippedSelected  int
	SkippedUnmatched int
	Selected         []SpecLine
}

// Validate enforces the report contract. The summary is always populated
// as far as parsing got, so callers can print it for failures too.
func Validate(data []byte, want Expectation) (ReportSummary, error) {
	report, err := decodeReport(data)
	if err != nil {
		return ReportSummary{}, err
	}
	summary, problems := inspectSpecs(report, want.Label)
	problems = append(problems, suiteProblems(report)...)
	if summary.Executed == 0 {
		problems = append(problems, fmt.Sprintf(
			"no executed It spec for label %q (%d selected skipped, %d unselected skipped)",
			want.Label, summary.SkippedSelected, summary.SkippedUnmatched))
	}
	problems = append(problems, requiredProblems(report, want)...)
	if len(problems) > 0 {
		return summary, errors.New(strings.Join(problems, "; "))
	}
	return summary, nil
}

func decodeReport(data []byte) (ginkgoReport, error) {
	reports, err := decodeReportArray(data)
	if err != nil {
		return ginkgoReport{}, err
	}
	if len(reports) != 1 {
		return ginkgoReport{}, fmt.Errorf("expected exactly one suite report, got %d", len(reports))
	}
	if err := checkSpecShape(reports[0]); err != nil {
		return ginkgoReport{}, err
	}
	return reports[0], nil
}

func decodeReportArray(data []byte) ([]ginkgoReport, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("report file is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var reports []ginkgoReport
	if err := dec.Decode(&reports); err != nil {
		return nil, fmt.Errorf("report is not a Ginkgo JSON report array: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("report has trailing data after the JSON array")
	}
	return reports, nil
}

// checkSpecShape fails on missing required data rather than guessing.
func checkSpecShape(report ginkgoReport) error {
	if report.SpecReports == nil {
		return errors.New("report has no SpecReports")
	}
	for i, spec := range report.SpecReports {
		if spec.LeafNodeType == "" || spec.State == "" {
			return fmt.Errorf("spec report %d lacks LeafNodeType or State", i)
		}
	}
	return nil
}

func suiteProblems(report ginkgoReport) []string {
	var problems []string
	if report.SuiteSucceeded != nil && !*report.SuiteSucceeded {
		problems = append(problems, "suite reports SuiteSucceeded=false")
	}
	for _, reason := range report.SpecialSuiteFailureReasons {
		problems = append(problems, "suite failure reason: "+strconv.Quote(reason))
	}
	return problems
}

// inspectSpecs applies rule 3: any failed, timed-out, panicked, interrupted or
// ambiguous spec fails the report, and executed specs must carry the label.
func inspectSpecs(report ginkgoReport, label string) (ReportSummary, []string) {
	in := specInspector{label: label, summary: ReportSummary{Total: len(report.SpecReports)}}
	for _, spec := range report.SpecReports {
		in.inspect(spec)
	}
	return in.summary, in.problems
}

type specInspector struct {
	label    string
	summary  ReportSummary
	problems []string
}

func (in *specInspector) inspect(spec ginkgoSpec) {
	switch {
	case slices.Contains(failureStates, spec.State):
		in.problemf("%s %s: %s", spec.LeafNodeType, strconv.Quote(spec.LeafNodeText), spec.State)
		if in.countsAsSelected(spec) {
			in.summary.Executed++
			in.record(spec)
		}
	case spec.State == statePassed:
		in.passed(spec)
	case spec.State == stateSkipped || spec.State == statePending:
		in.skipped(spec)
	default:
		in.problemf("%s %s: ambiguous state %q",
			spec.LeafNodeType, strconv.Quote(spec.LeafNodeText), spec.State)
	}
}

func (in *specInspector) passed(spec ginkgoSpec) {
	if spec.LeafNodeType != nodeTypeIt {
		return
	}
	if !specHasLabel(spec, in.label) {
		in.problemf("spec %s executed without label %q", strconv.Quote(spec.LeafNodeText), in.label)
		return
	}
	in.summary.Executed++
	in.summary.Passed++
	in.record(spec)
}

func (in *specInspector) skipped(spec ginkgoSpec) {
	switch {
	case spec.LeafNodeType != nodeTypeIt:
	case specHasLabel(spec, in.label):
		in.summary.SkippedSelected++
		in.record(spec)
	default:
		in.summary.SkippedUnmatched++
	}
}

func (in *specInspector) countsAsSelected(spec ginkgoSpec) bool {
	return spec.LeafNodeType == nodeTypeIt && specHasLabel(spec, in.label)
}

func (in *specInspector) record(spec ginkgoSpec) {
	in.summary.Selected = append(in.summary.Selected, SpecLine{spec.LeafNodeText, spec.State})
}

func (in *specInspector) problemf(format string, args ...any) {
	in.problems = append(in.problems, fmt.Sprintf(format, args...))
}

// requiredProblems applies rule 4 by identity: each required title must appear
// exactly once among the selected It specs and must not be skipped.
func requiredProblems(report ginkgoReport, want Expectation) []string {
	var problems []string
	for _, title := range want.Required {
		if p := requiredProblem(report, want.Label, title); p != "" {
			problems = append(problems, p)
		}
	}
	return problems
}

func requiredProblem(report ginkgoReport, label, title string) string {
	found := specsByIdentity(report, label, title)
	quoted := strconv.Quote(title)
	switch {
	case len(found) == 0:
		return "required spec missing: " + quoted
	case len(found) > 1:
		return fmt.Sprintf("required spec duplicated (%d reports): %s", len(found), quoted)
	case found[0].State == stateSkipped || found[0].State == statePending:
		return "required spec skipped: " + quoted
	}
	return ""
}

// specsByIdentity returns the It specs with leaf text title under label.
func specsByIdentity(report ginkgoReport, label, title string) []ginkgoSpec {
	var found []ginkgoSpec
	for _, spec := range report.SpecReports {
		if spec.LeafNodeType == nodeTypeIt && spec.LeafNodeText == title &&
			specHasLabel(spec, label) {
			found = append(found, spec)
		}
	}
	return found
}

// specHasLabel matches like Ginkgo's label filter for a plain label: exact,
// case-insensitive membership in the leaf or any container label set.
func specHasLabel(spec ginkgoSpec, label string) bool {
	match := func(labels []string) bool {
		return slices.ContainsFunc(
			labels,
			func(l string) bool { return strings.EqualFold(l, label) },
		)
	}
	if match(spec.LeafNodeLabels) {
		return true
	}
	return slices.ContainsFunc(spec.ContainerHierarchyLabels, match)
}

// formatSummary renders the succinct per-spec status; it never includes
// environment data.
func FormatSummary(path string, s ReportSummary) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"e2e report %s: %d specs, %d executed (%d passed), %d selected skipped, %d unselected skipped\n",
		path,
		s.Total,
		s.Executed,
		s.Passed,
		s.SkippedSelected,
		s.SkippedUnmatched,
	)
	for _, line := range s.Selected {
		fmt.Fprintf(&b, "  %-11s %s\n", line.State, strconv.Quote(line.Text))
	}
	return b.String()
}
