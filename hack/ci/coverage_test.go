package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// coverageRepoRoot is the repository root relative to this package directory.
const coverageRepoRoot = "../.."

const coverageGoListFormat = "{{.ImportPath}}|{{.Dir}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}"

var (
	// coverageLabelCall matches `Label(...)` and `ginkgo.Label(...)`.
	coverageLabelCall = regexp.MustCompile(`\bLabel\(([^)]*)\)`)
	coverageStringLit = regexp.MustCompile(`"([^"]+)"`)
	// coverageStringConst matches a bare `name = "value"` assignment (not a
	// field such as `fixture.label = ...`), used to resolve labels passed as
	// named constants such as Label(suiteLabel).
	coverageStringConst = regexp.MustCompile(`(?m)(?:^|[^.\w])([A-Za-z_]\w*)\s*=\s*"([^"]+)"`)
	coverageSelector    = regexp.MustCompile(`\.[A-Za-z_]\w*`)
	coverageIdent       = regexp.MustCompile(`[A-Za-z_]\w*`)
	// coverageNonUnitDir matches directories whose tests are not unit tests.
	// It mirrors the rule the unit tier uses to discover packages.
	coverageNonUnitDir = regexp.MustCompile(`(^|/)(test|e2e)(/|$)`)
)

// coverageSuite guards that every Ginkgo label, Go test package and manifest
// entry is assigned to a CI tier, so no test is silently orphaned.
type coverageSuite struct {
	suite.Suite
	root     string
	manifest *Manifest
	labels   map[string]bool
}

func TestCoverageSuite(t *testing.T) {
	suite.Run(t, new(coverageSuite))
}

func (s *coverageSuite) SetupSuite() {
	root, err := filepath.Abs(coverageRepoRoot)
	s.Require().NoError(err)
	s.root = root

	m, err := LoadManifest()
	s.Require().NoError(err)
	s.manifest = m

	labels, err := coverageScanLabels(filepath.Join(root, "e2e"))
	s.Require().NoError(err)
	s.labels = labels
}

// Footgun: an e2e spec gets a new Label that no suite selects, so the spec
// never runs in PR CI and nobody notices.
func (s *coverageSuite) TestEveryGinkgoLabelIsAssigned() {
	assigned := s.assignedLabels()

	var missing []string
	for _, label := range sortedKeys(s.labels) {
		if !assigned[label] {
			missing = append(missing, fmt.Sprintf(
				"Ginkgo label %q is used in e2e/ but not assigned to a tier; "+
					"add it to hack/ci/suites.yaml (e2e.suites, or e2e.ungated with a reason)",
				label,
			))
		}
	}
	if len(missing) > 0 {
		s.Failf("unassigned Ginkgo labels", "%s", strings.Join(missing, "\n"))
	}
}

// Footgun: a label is renamed or removed in e2e/ but its manifest entry stays,
// so the CI job selects nothing and reports a green result.
func (s *coverageSuite) TestEveryManifestLabelExistsInE2E() {
	var stale []string
	for _, label := range s.manifestLabels() {
		if !s.labels[label] {
			stale = append(stale, fmt.Sprintf(
				"label %q is in hack/ci/suites.yaml but no Label(...) in e2e/ uses it; remove the stale entry or restore the label",
				label,
			))
		}
	}
	if len(stale) > 0 {
		s.Failf("stale manifest labels", "%s", strings.Join(stale, "\n"))
	}
}

// Footgun: a new Go package with tests lands outside every unit step and
// exclusion, so `go test` never runs it in CI.
func (s *coverageSuite) TestEveryTestPackageIsAccounted() {
	if _, err := exec.LookPath("go"); err != nil {
		s.T().Skip("go tool not available")
	}
	dirs, err := coverageTestPackageDirs(s.root)
	s.Require().NoError(err)

	patterns := s.unitPatterns()
	var unaccounted []string
	for _, dir := range dirs {
		pkg := coverageDotPath(dir)
		if coverageNonUnitDir.MatchString(dir) && !coverageAnyMatch(patterns, pkg) {
			unaccounted = append(unaccounted, pkg)
		}
	}
	if len(unaccounted) > 0 {
		s.Failf(
			"packages with tests not assigned to a tier",
			"add these packages to a unit step in hack/ci/suites.yaml "+
				"(unit.steps packages, or unit.excluded with a reason):\n%s",
			strings.Join(unaccounted, "\n"),
		)
	}
}

// Footgun: a unit step or excluded entry names a package that was moved or
// deleted, so the step quietly tests nothing.
func (s *coverageSuite) TestUnitEntriesExistOnDisk() {
	var missing []string
	for _, pkg := range s.literalUnitPackages() {
		dir := filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(pkg, "./")))
		if !coverageIsDir(dir) {
			missing = append(missing, pkg)
		}
	}
	if len(missing) > 0 {
		s.Failf(
			"unit entries point at missing packages",
			"remove or fix these entries in hack/ci/suites.yaml:\n%s",
			strings.Join(missing, "\n"),
		)
	}
}

func (s *coverageSuite) assignedLabels() map[string]bool {
	assigned := map[string]bool{}
	for _, label := range s.manifestLabels() {
		assigned[label] = true
	}
	return assigned
}

func (s *coverageSuite) manifestLabels() []string {
	var labels []string
	for _, entry := range s.manifest.E2E.Suites {
		labels = append(labels, entry.Label)
	}
	for _, ungated := range s.manifest.E2E.Ungated {
		labels = append(labels, ungated.Label)
	}
	return labels
}

// unitPatterns returns every package pattern that a unit step or exclusion names.
func (s *coverageSuite) unitPatterns() []string {
	var patterns []string
	for _, step := range s.manifest.Unit.Steps {
		patterns = append(patterns, step.Packages...)
	}
	for _, excluded := range s.manifest.Unit.Excluded {
		patterns = append(patterns, excluded.Package)
	}
	return patterns
}

// literalUnitPackages returns the step packages and exclusions without a
// `/...` suffix, which must name a real directory.
func (s *coverageSuite) literalUnitPackages() []string {
	var pkgs []string
	for _, pattern := range s.unitPatterns() {
		if !strings.HasSuffix(pattern, "/...") {
			pkgs = append(pkgs, pattern)
		}
	}
	return pkgs
}

// coverageScanLabels collects every label passed to a Label(...) call under
// dir. Literal strings count, and so do identifiers that name a string
// constant somewhere under dir. Dot-imported and qualified calls both match.
func coverageScanLabels(dir string) (map[string]bool, error) {
	sources, err := coverageReadGoFiles(dir)
	if err != nil {
		return nil, err
	}
	consts := map[string]string{}
	for _, data := range sources {
		for _, m := range coverageStringConst.FindAllSubmatch(data, -1) {
			consts[string(m[1])] = string(m[2])
		}
	}
	labels := map[string]bool{}
	for _, data := range sources {
		coverageCollectLabels(data, consts, labels)
	}
	return labels, nil
}

func coverageReadGoFiles(dir string) ([][]byte, error) {
	var sources [][]byte
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		sources = append(sources, data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan e2e sources: %w", err)
	}
	return sources, nil
}

func coverageCollectLabels(data []byte, consts map[string]string, labels map[string]bool) {
	for _, call := range coverageLabelCall.FindAllSubmatch(data, -1) {
		args := call[1]
		for _, lit := range coverageStringLit.FindAllSubmatch(args, -1) {
			labels[string(lit[1])] = true
		}
		// Selectors (provider.label) are struct fields, not package constants.
		bare := coverageSelector.ReplaceAll(coverageStringLit.ReplaceAll(args, nil), nil)
		for _, ident := range coverageIdent.FindAll(bare, -1) {
			if value, ok := consts[string(ident)]; ok {
				labels[value] = true
			}
		}
	}
}

// coverageTestPackageDirs returns the root-relative directories of packages
// that have _test.go files, as reported by `go list`.
func coverageTestPackageDirs(root string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "list", "-f", coverageGoListFormat, "./...")
	cmd.Dir = root
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list ./...: %w\n%s", err, stderr.String())
	}
	return coverageParseGoList(string(out), root)
}

// coverageParseGoList keeps the root-relative directories of packages that
// have test files.
func coverageParseGoList(output, root string) ([]string, error) {
	var dirs []string
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		dir, hasTests, err := coverageParseGoListLine(line, root)
		if err != nil {
			return nil, err
		}
		if hasTests {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// coverageParseGoListLine parses one `importpath|dir|tests|xtests` line.
func coverageParseGoListLine(line, root string) (string, bool, error) {
	fields := strings.Split(line, "|")
	if len(fields) != 4 {
		return "", false, fmt.Errorf("unexpected go list line %q", line)
	}
	testFiles, errTest := strconv.Atoi(fields[2])
	xTestFiles, errX := strconv.Atoi(fields[3])
	if errTest != nil || errX != nil {
		return "", false, fmt.Errorf("unexpected go list counts in %q", line)
	}
	rel, err := filepath.Rel(root, fields[1])
	if err != nil {
		return "", false, fmt.Errorf("relative path of %s: %w", fields[1], err)
	}
	return filepath.ToSlash(rel), testFiles+xTestFiles > 0, nil
}

// coverageDotPath converts a root-relative directory into the `./dir` form
// used by manifest package entries.
func coverageDotPath(rel string) string {
	if rel == "." {
		return "."
	}
	return "./" + rel
}

func coverageAnyMatch(patterns []string, pkg string) bool {
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		return coverageMatches(pattern, pkg)
	})
}

// coverageMatches compares a manifest entry with a `./dir` package path. A
// `/...` suffix matches the directory and everything below it.
func coverageMatches(pattern, pkg string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "/..."); ok {
		return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
	}
	return pattern == pkg
}

func coverageIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
