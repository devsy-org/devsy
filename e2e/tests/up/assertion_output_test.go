package up

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"

	"github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
)

var maskedAssertionFiles = []string{
	"fixture_command.go",
	"helper.go",
	"provider_kubernetes_dockerless_path.go",
}

var rawOutputMatchers = map[string]bool{
	"ContainSubstring": true,
	"MatchRegexp":      true,
	"Equal":            true,
	"HavePrefix":       true,
	"HaveSuffix":       true,
}

var outputNames = map[string]bool{
	"output": true, "out": true, "stdout": true, "stderr": true,
}

func TestGomegaPrintsTheActualValueOfAFailedSubstringMatch(t *testing.T) {
	previousGomega := gomega.Default
	gomega.Default = gomega.NewWithT(t)
	t.Cleanup(func() { gomega.Default = previousGomega })
	failures := gomega.InterceptGomegaFailures(func() {
		gomega.Expect("raw-output-marker").To(gomega.ContainSubstring("absent"))
	})
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "raw-output-marker")
}

func TestUpSpecsDoNotAssertDirectlyOnRawCommandOutput(t *testing.T) {
	var offenders []string
	for _, name := range maskedAssertionFiles {
		source, err := os.ReadFile(name) //nolint:gosec // fixed in-package source names
		require.NoError(t, err)
		parsed, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		require.NoError(t, err)
		ast.Inspect(parsed, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok && assertsRawOutput(call) {
				offenders = append(offenders, name)
			}
			return true
		})
	}
	require.Empty(t, offenders, "assert on a boolean and report fixtureDiagnostic output")
}

func assertsRawOutput(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "To" && selector.Sel.Name != "NotTo") || len(call.Args) == 0 {
		return false
	}
	expect, ok := selector.X.(*ast.CallExpr)
	return ok && len(expect.Args) == 1 && isRawOutputMatcher(call.Args[0]) &&
		mentionsOutput(expect.Args[0])
}

func mentionsOutput(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		return outputNames[value.Name]
	case *ast.CallExpr:
		return len(value.Args) == 1 && mentionsOutput(value.Args[0])
	}
	return false
}

func isRawOutputMatcher(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && rawOutputMatchers[selector.Sel.Name]
}
