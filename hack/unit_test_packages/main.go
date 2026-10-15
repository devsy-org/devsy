package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const goListFormat = "{{.ImportPath}}|{{.Dir}}"

var (
	errNoUnitTestPackages  = errors.New("no unit-test packages found")
	errOutsideRepository   = errors.New("package directory is outside the repository")
	errMalformedGoList     = errors.New("unexpected go list line")
	excludedPackagePattern = regexp.MustCompile(`(^|/)(test|e2e)(/|$)`)
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: unit_test_packages (run from the repository root)")
		return 2
	}
	root, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	goList, err := runGoList(stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	packages, err := filterUnitTestPackages(goList, root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	for _, p := range packages {
		_, _ = fmt.Fprintln(stdout, p)
	}
	return 0
}

func runGoList(stderr io.Writer) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command("go", "list", "-f", goListFormat, "./...")
	cmd.Stdout = &out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go list: %w", err)
	}
	return out.String(), nil
}

// filterUnitTestPackages takes `go list -f '{{.ImportPath}}|{{.Dir}}'` output
// and returns the import paths of packages under root, skipping any package
// whose directory has a test or e2e path segment below root.
func filterUnitTestPackages(goList, root string) ([]string, error) {
	var packages []string
	for line := range strings.SplitSeq(goList, "\n") {
		if line == "" {
			continue
		}
		importPath, dir, ok := strings.Cut(line, "|")
		if !ok {
			return nil, fmt.Errorf("%w: %q", errMalformedGoList, line)
		}
		if !isUnderRoot(dir, root) {
			return nil, fmt.Errorf("%w: %s", errOutsideRepository, dir)
		}
		relative := filepath.ToSlash(strings.TrimPrefix(dir, root))
		if excludedPackagePattern.MatchString(relative) {
			continue
		}
		packages = append(packages, importPath)
	}
	if len(packages) == 0 {
		return nil, errNoUnitTestPackages
	}
	return packages, nil
}

// isUnderRoot reports whether dir is root or a directory inside it.
// A sibling sharing root as a string prefix (root+"2") is not under root.
func isUnderRoot(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}
