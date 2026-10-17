package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/onsi/ginkgo/v2/types"
	"github.com/stretchr/testify/suite"
)

const (
	labelsWorkflowFixture = "testdata/workflow_labels.yml"
	testWorkflow          = "wf.yml"
	testReport            = "report.json"
	jsonReportFlag        = "-ginkgo.json-report="
	labelX                = "up-x"
	labelY                = "up-y"
	specTwoText           = "suite two"
)

type LabelsSuite struct{ suite.Suite }

func TestLabelsSuite(t *testing.T) { suite.Run(t, new(LabelsSuite)) }

func itSpec(containerLabels []string, leafLabels []string, text string) types.SpecReport {
	return types.SpecReport{
		LeafNodeType:             types.NodeTypeIt,
		ContainerHierarchyTexts:  []string{"suite"},
		ContainerHierarchyLabels: [][]string{containerLabels},
		LeafNodeLabels:           leafLabels,
		LeafNodeText:             text,
		LeafNodeLocation:         types.CodeLocation{FileName: "x_test.go", LineNumber: 7},
	}
}

func filtersFor(exprs ...string) []labelFilter {
	filters := make([]labelFilter, 0, len(exprs))
	for _, e := range exprs {
		filters = append(filters, labelFilter{source: "test", expr: e})
	}
	return filters
}

func (s *LabelsSuite) TestEvaluateLabelCoverage() {
	specs := types.SpecReports{
		itSpec([]string{labelX}, []string{"fast"}, "one"),
		itSpec(nil, []string{"slow"}, "two"),
		itSpec([]string{labelY}, nil, "three"),
		{LeafNodeType: types.NodeTypeBeforeAll, ContainerHierarchyLabels: [][]string{{labelX}}},
	}
	tests := []struct {
		name          string
		exprs         []string
		wantSpecs     int
		wantUncovered []string
		wantDead      []string
	}{
		{
			name:          "container label selects leaf spec",
			exprs:         []string{labelX},
			wantSpecs:     3,
			wantUncovered: []string{specTwoText, "suite three"},
		},
		{
			name:          "conjunction across container and leaf labels",
			exprs:         []string{labelX + " && fast", "!slow", labelY},
			wantSpecs:     3,
			wantUncovered: []string{specTwoText},
		},
		{
			name:          "conjunction that matches nothing is dead",
			exprs:         []string{labelX + " && slow", labelX, "!slow"},
			wantSpecs:     3,
			wantUncovered: []string{specTwoText},
			wantDead:      []string{labelX + " && slow"},
		},
		{
			name:          "non-It nodes do not count as specs",
			exprs:         []string{labelX, labelY, "fast", "slow"},
			wantSpecs:     3,
			wantUncovered: nil,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, err := evaluateLabelCoverage(specs, filtersFor(tc.exprs...))
			s.Require().NoError(err)
			s.Equal(tc.wantSpecs, got.specs)

			var uncovered []string
			for _, spec := range got.uncovered {
				uncovered = append(uncovered, spec.FullText())
			}
			s.Equal(tc.wantUncovered, uncovered)

			var dead []string
			for _, f := range got.dead {
				dead = append(dead, f.expr)
			}
			s.Equal(tc.wantDead, dead)
		})
	}
}

func (s *LabelsSuite) TestEvaluateLabelCoverageErrors() {
	it := types.SpecReports{itSpec([]string{labelX}, nil, "one")}
	s.Run("report without It specs", func() {
		_, err := evaluateLabelCoverage(types.SpecReports{
			{LeafNodeType: types.NodeTypeBeforeAll},
		}, filtersFor(labelX))
		s.ErrorContains(err, "report contains no It specs")
	})
	s.Run("unparseable filter names its source", func() {
		_, err := evaluateLabelCoverage(it, []labelFilter{{source: "pr-ci.yml job e2e", expr: "("}})
		s.ErrorContains(err, "pr-ci.yml job e2e")
	})
}

func (s *LabelsSuite) TestProblemsReportsUncoveredAndDead() {
	coverage, err := evaluateLabelCoverage(types.SpecReports{
		itSpec(nil, nil, "orphan"),
	}, filtersFor(labelX))
	s.Require().NoError(err)

	err = coverage.problems()
	s.Require().Error(err)
	s.Contains(err.Error(), "x_test.go:7: suite orphan")
	s.Contains(err.Error(), `"up-x" from test`)
}

func (s *LabelsSuite) TestProblemsEmptyWhenCovered() {
	coverage, err := evaluateLabelCoverage(types.SpecReports{
		itSpec([]string{labelX}, nil, "one"),
	}, filtersFor(labelX))
	s.Require().NoError(err)
	s.NoError(coverage.problems())
}

func (s *LabelsSuite) TestWorkflowLabelFilters() {
	data, err := os.ReadFile(labelsWorkflowFixture)
	s.Require().NoError(err)

	filters, err := workflowLabelFilters(testWorkflow, data)
	s.Require().NoError(err)

	var exprs []string
	for _, f := range filters {
		exprs = append(exprs, f.expr)
	}
	// Matrix rows without a label are ignored; ${{ }} run values are skipped.
	s.Equal([]string{
		"up-alpha",
		"up-beta",
		"up-gamma && !slow",
		"up-delta",
		"up-eps",
		"up-zeta",
	}, exprs)
	s.Contains(filters[2].source, "job e2e")
}

func (s *LabelsSuite) TestWorkflowLabelFiltersRejectsUnverifiableMatrix() {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "matrix expression is not a literal",
			yaml: "jobs:\n  e2e:\n    strategy:\n      matrix:\n" +
				"        include:\n          - label: ${{ inputs.label }}\n",
			wantErr: "must be a literal",
		},
		{
			name:    "matrix is not a mapping",
			yaml:    "jobs:\n  e2e:\n    strategy:\n      matrix: ${{ fromJSON(x) }}\n",
			wantErr: "strategy.matrix must be a mapping",
		},
		{
			name:    "invalid yaml",
			yaml:    "jobs: [",
			wantErr: "decode workflow",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			_, err := workflowLabelFilters(testWorkflow, []byte(tc.yaml))
			s.ErrorContains(err, tc.wantErr)
		})
	}
}

func (s *LabelsSuite) TestRunCheckLabelsWithReport() {
	workflow, err := os.ReadFile(labelsWorkflowFixture)
	s.Require().NoError(err)

	covering := types.Report{
		SuiteSucceeded: true,
		SpecReports: types.SpecReports{
			itSpec([]string{"up-alpha"}, nil, "a"),
			itSpec([]string{"up-beta"}, nil, "b"),
			itSpec([]string{"up-gamma"}, nil, "c"),
			itSpec([]string{"up-delta"}, nil, "d"),
			itSpec([]string{"up-eps"}, nil, "e"),
			itSpec([]string{"up-zeta"}, nil, "f"),
		},
	}

	s.Run("covered specs succeed and print the count", func() {
		rig := runenvtest.NewRig(runenv.GoosLinux, nil)
		rig.FS.Data[testWorkflow] = workflow
		rig.FS.Data[testReport] = mustJSON(s, []types.Report{covering})

		err := runCheckLabels(s.T().Context(), rig.Dependencies, testReport, []string{testWorkflow})
		s.Require().NoError(err)
		s.Contains(rig.Output.String(), "label coverage: 6 specs, 6 filters")
	})

	s.Run("uncovered spec fails with its location", func() {
		rig := runenvtest.NewRig(runenv.GoosLinux, nil)
		rig.FS.Data[testWorkflow] = workflow
		report := covering
		report.SpecReports = append(
			types.SpecReports{itSpec(nil, nil, "loose")},
			report.SpecReports...)
		rig.FS.Data[testReport] = mustJSON(s, []types.Report{report})

		err := runCheckLabels(s.T().Context(), rig.Dependencies, testReport, []string{testWorkflow})
		s.ErrorContains(err, "x_test.go:7: suite loose")
	})

	s.Run("missing workflow is an error", func() {
		rig := runenvtest.NewRig(runenv.GoosLinux, nil)
		rig.FS.Data[testReport] = mustJSON(s, []types.Report{covering})

		err := runCheckLabels(s.T().Context(), rig.Dependencies, testReport, []string{"absent.yml"})
		s.ErrorContains(err, "read workflow absent.yml")
	})
}

func (s *LabelsSuite) TestReadSpecReportGeneratesReport() {
	rig := runenvtest.NewRig(runenv.GoosLinux, nil)
	rig.Runner.Handle = func(c runenv.Command) (runenv.CommandResult, error) {
		if c.Path == "go" {
			return runenv.CommandResult{}, nil
		}
		for _, arg := range c.Args {
			if path, ok := strings.CutPrefix(arg, jsonReportFlag); ok {
				rig.FS.Data[path] = []byte("[]")
			}
		}
		return runenv.CommandResult{}, nil
	}

	data, err := readSpecReport(s.T().Context(), rig.Dependencies, "")
	s.Require().NoError(err)
	s.Equal("[]", string(data))

	s.Require().Len(rig.Runner.Calls, 2)
	build, list := rig.Runner.Calls[0], rig.Runner.Calls[1]
	s.Equal("go", build.Path)
	s.Equal([]string{"test", "-c", "-o", build.Args[3], "./e2e"}, build.Args)
	s.Equal(build.Args[3], list.Path)
	s.Equal("e2e", list.Dir)
	s.Contains(list.Args, "-test.run=^TestRunE2ETests$")
	s.Contains(list.Args, "-ginkgo.dry-run")
}

func (s *LabelsSuite) TestReadSpecReportWindowsBinaryName() {
	rig := runenvtest.NewRig(runenv.GoosWindows, nil)
	rig.Runner.Handle = func(c runenv.Command) (runenv.CommandResult, error) {
		if c.Path != "go" {
			for _, arg := range c.Args {
				if path, ok := strings.CutPrefix(arg, jsonReportFlag); ok {
					rig.FS.Data[path] = []byte("[]")
				}
			}
		}
		return runenv.CommandResult{}, nil
	}

	_, err := readSpecReport(s.T().Context(), rig.Dependencies, "")
	s.Require().NoError(err)
	s.True(strings.HasSuffix(rig.Runner.Calls[0].Args[3], ".exe"))
}

func (s *LabelsSuite) TestReadSpecReportFailsOnStepExit() {
	rig := runenvtest.NewRig(runenv.GoosLinux, nil)
	rig.Runner.Handle = func(runenv.Command) (runenv.CommandResult, error) {
		return runenv.CommandResult{ExitCode: 1}, nil
	}

	_, err := readSpecReport(s.T().Context(), rig.Dependencies, "")
	s.ErrorContains(err, "build e2e test binary: exit code 1")
}

func mustJSON(s *LabelsSuite, v any) []byte {
	data, err := json.Marshal(v)
	s.Require().NoError(err)
	return data
}
