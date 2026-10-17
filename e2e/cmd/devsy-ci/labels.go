package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/onsi/ginkgo/v2/types"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	checkLabelsCommandName = "check-labels"
	e2ePackage             = "./e2e"
	e2eDir                 = "e2e"
	e2eSuiteRun            = "^TestRunE2ETests$"
	buildTimeout           = 15 * time.Minute
	dryRunTimeout          = 10 * time.Minute
	ginkgoExpressionMarker = "${{"
)

// defaultLabelWorkflows lists every workflow that selects e2e specs by label.
var defaultLabelWorkflows = []string{
	".github/workflows/pr-ci.yml",
	".github/workflows/docker-buildkit-windows.yml",
}

// labelFilterArg matches a literal --ginkgo.label-filter= value in a run script,
// quoted with " or ', or unquoted (shell or PowerShell).
var labelFilterArg = regexp.MustCompile(
	`label-filter=(?:"([^"]*)"|'([^']*)'|([^\s"'\x60]+))`,
)

type labelFilter struct {
	source string
	expr   string
	match  types.LabelFilter
}

type workflowDoc struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Strategy workflowStrategy `yaml:"strategy"`
	Steps    []workflowStep   `yaml:"steps"`
}

type workflowStrategy struct {
	Matrix any `yaml:"matrix"`
}

type workflowStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

type labelCoverage struct {
	specs     int
	uncovered []types.SpecReport
	dead      []labelFilter
}

func newCheckLabelsCommand(deps *runenv.Dependencies) *cobra.Command {
	var reportPath string
	var workflows []string
	cmd := &cobra.Command{
		Use:   checkLabelsCommandName,
		Short: "Require every Ginkgo spec to be selected by a CI label filter",
		Long: "Requires every It spec in the e2e suite to match at least one CI label " +
			"filter, and every filter to select at least one spec. Run from the repository root.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCheckLabels(cmd.Context(), deps, reportPath, workflows)
		},
	}
	cmd.Flags().StringVar(&reportPath, "report", "",
		"Ginkgo JSON dry-run report; the e2e suite is built and listed when empty")
	cmd.Flags().StringSliceVar(&workflows, "workflow", slices.Clone(defaultLabelWorkflows),
		"workflow file to read label filters from (repeatable)")
	return cmd
}

func runCheckLabels(
	ctx context.Context,
	deps *runenv.Dependencies,
	reportPath string,
	workflows []string,
) error {
	filters, err := loadLabelFilters(deps, workflows)
	if err != nil {
		return runenv.NewStageError(runenv.CodeLabelCoverageInvalid, checkLabelsCommandName, err)
	}
	data, err := readSpecReport(ctx, deps, reportPath)
	if err != nil {
		return runenv.NewStageError(runenv.CodeLabelCoverageInvalid, checkLabelsCommandName, err)
	}
	report, err := decodeSuiteReport(data)
	if err != nil {
		return runenv.NewStageError(runenv.CodeLabelCoverageInvalid, checkLabelsCommandName, err)
	}
	coverage, err := evaluateLabelCoverage(report.SpecReports, filters)
	if err != nil {
		return runenv.NewStageError(runenv.CodeLabelCoverageInvalid, checkLabelsCommandName, err)
	}
	if err := coverage.problems(); err != nil {
		return runenv.NewStageError(runenv.CodeLabelCoverageInvalid, checkLabelsCommandName, err)
	}
	if _, err := fmt.Fprintf(
		deps.Out, "label coverage: %d specs, %d filters\n", coverage.specs, len(filters),
	); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func loadLabelFilters(deps *runenv.Dependencies, workflows []string) ([]labelFilter, error) {
	var filters []labelFilter
	for _, path := range workflows {
		data, err := deps.Files.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read workflow %s: %w", path, err)
		}
		found, err := workflowLabelFilters(path, data)
		if err != nil {
			return nil, err
		}
		filters = append(filters, found...)
	}
	if len(filters) == 0 {
		return nil, errors.New("no CI label filters found in the workflows")
	}
	return filters, nil
}

// workflowLabelFilters collects matrix labels and literal --ginkgo.label-filter
// values. Expressions such as ${{ matrix.label }} are skipped; their values
// come from the matrix rows, which are already collected.
func workflowLabelFilters(path string, data []byte) ([]labelFilter, error) {
	var doc workflowDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode workflow %s: %w", path, err)
	}
	var filters []labelFilter
	for _, jobName := range sortedKeys(doc.Jobs) {
		jobFilters, err := jobLabelFilters(path, jobName, doc.Jobs[jobName])
		if err != nil {
			return nil, err
		}
		filters = append(filters, jobFilters...)
	}
	return filters, nil
}

// jobLabelFilters returns the matrix labels and literal label-filter values of
// one job.
func jobLabelFilters(path, jobName string, job workflowJob) ([]labelFilter, error) {
	labels, err := matrixLabels(job.Strategy.Matrix)
	if err != nil {
		return nil, fmt.Errorf("%s job %s: %w", path, jobName, err)
	}
	var filters []labelFilter
	for _, label := range labels {
		filters = append(filters, labelFilter{
			source: fmt.Sprintf("%s job %s matrix", path, jobName),
			expr:   label,
		})
	}
	for _, step := range job.Steps {
		for _, m := range labelFilterArg.FindAllStringSubmatch(step.Run, -1) {
			expr := firstNonEmpty(m[1:])
			if expr == "" || strings.Contains(expr, ginkgoExpressionMarker) {
				continue
			}
			filters = append(filters, labelFilter{
				source: fmt.Sprintf("%s job %s step %q", path, jobName, step.Name),
				expr:   expr,
			})
		}
	}
	return filters, nil
}

// matrixLabels returns the label of each matrix include row. Rows without a
// label (for example build-cli rows) are not e2e filters and are ignored.
func matrixLabels(matrix any) ([]string, error) {
	if matrix == nil {
		return nil, nil
	}
	m, ok := matrix.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("strategy.matrix must be a mapping to be checked, got %T", matrix)
	}
	include, _ := m["include"].([]any)
	var labels []string
	for _, row := range include {
		fields, ok := row.(map[string]any)
		if !ok {
			continue
		}
		label, ok := fields["label"].(string)
		if !ok || label == "" {
			continue
		}
		if strings.Contains(label, ginkgoExpressionMarker) {
			return nil, fmt.Errorf("matrix label %q must be a literal", label)
		}
		labels = append(labels, label)
	}
	return labels, nil
}

func compileLabelFilters(filters []labelFilter) ([]labelFilter, error) {
	compiled := make([]labelFilter, len(filters))
	for i, f := range filters {
		match, err := types.ParseLabelFilter(f.expr)
		if err != nil {
			return nil, fmt.Errorf(
				"parse label filter %q from %s: %w",
				f.expr,
				f.source,
				err,
			)
		}
		f.match = match
		compiled[i] = f
	}
	return compiled, nil
}

// recordSpecHits adds one hit to every filter that selects spec and reports
// whether any filter selected it.
func recordSpecHits(spec types.SpecReport, filters []labelFilter, hits []int) bool {
	labels := spec.Labels()
	selected := false
	for i, f := range filters {
		if f.match(labels) {
			hits[i]++
			selected = true
		}
	}
	return selected
}

func evaluateLabelCoverage(specs types.SpecReports, filters []labelFilter) (labelCoverage, error) {
	compiled, err := compileLabelFilters(filters)
	if err != nil {
		return labelCoverage{}, err
	}

	var coverage labelCoverage
	hits := make([]int, len(compiled))
	for _, spec := range specs {
		if spec.LeafNodeType != types.NodeTypeIt {
			continue
		}
		coverage.specs++
		if !recordSpecHits(spec, compiled, hits) {
			coverage.uncovered = append(coverage.uncovered, spec)
		}
	}
	if coverage.specs == 0 {
		return labelCoverage{}, errors.New("report contains no It specs")
	}
	for i, n := range hits {
		if n == 0 {
			coverage.dead = append(coverage.dead, compiled[i])
		}
	}
	return coverage, nil
}

func (c labelCoverage) problems() error {
	if len(c.uncovered) == 0 && len(c.dead) == 0 {
		return nil
	}
	var b strings.Builder
	if len(c.uncovered) > 0 {
		fmt.Fprintf(&b, "%d spec(s) selected by no CI label filter:\n", len(c.uncovered))
		for _, spec := range c.uncovered {
			loc := spec.LeafNodeLocation
			fmt.Fprintf(&b, "  %s:%d: %s\n", loc.FileName, loc.LineNumber, spec.FullText())
		}
	}
	if len(c.dead) > 0 {
		fmt.Fprintf(&b, "%d label filter(s) select zero specs:\n", len(c.dead))
		for _, f := range c.dead {
			fmt.Fprintf(&b, "  %q from %s\n", f.expr, f.source)
		}
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

func readSpecReport(
	ctx context.Context,
	deps *runenv.Dependencies,
	reportPath string,
) ([]byte, error) {
	if reportPath != "" {
		data, err := deps.Files.ReadFile(reportPath)
		if err != nil {
			return nil, fmt.Errorf("read Ginkgo report: %w", err)
		}
		return data, nil
	}
	dir, err := os.MkdirTemp("", "devsy-ci-labels-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	binary := filepath.Join(dir, "e2e.test")
	if deps.GOOS == runenv.GoosWindows {
		binary += ".exe"
	}
	jsonPath := filepath.Join(dir, "report.json")

	if err := runStep(ctx, deps, runenv.Command{
		Path:    "go",
		Args:    []string{"test", "-c", "-o", binary, e2ePackage},
		Timeout: buildTimeout,
	}, "build e2e test binary"); err != nil {
		return nil, err
	}
	if err := runStep(ctx, deps, runenv.Command{
		Path: binary,
		Args: []string{
			"-test.run=" + e2eSuiteRun,
			"-ginkgo.dry-run",
			"-ginkgo.no-color",
			"-ginkgo.json-report=" + jsonPath,
		},
		Dir:     e2eDir,
		Timeout: dryRunTimeout,
	}, "list e2e specs"); err != nil {
		return nil, err
	}
	data, err := deps.Files.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("read generated Ginkgo report: %w", err)
	}
	return data, nil
}

func runStep(
	ctx context.Context,
	deps *runenv.Dependencies,
	cmd runenv.Command,
	step string,
) error {
	cmd.Stdout = deps.Err
	cmd.Stderr = deps.Err
	result, err := deps.Run.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("%s: %w", step, err)
	}
	if result.TimedOut {
		return fmt.Errorf("%s: timed out", step)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s: exit code %d", step, result.ExitCode)
	}
	return nil
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
