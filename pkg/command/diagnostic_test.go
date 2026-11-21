package command

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskkillDiagnosticMasksFoldedOutput(t *testing.T) {
	const fixture = "synthetic-taskkill-continuation"
	cause := errors.New("operation failed")
	err := formatTaskkillDiagnostic(
		42,
		cause,
		[]byte("public diagnostic\nAuthorization: Bearer\n "+fixture),
	)
	require.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "/PID 42")
	assert.Contains(t, err.Error(), "public diagnostic")
	assert.NotContains(t, err.Error(), fixture)
}

func TestTaskkillDiagnosticMasksBeforeTruncating(t *testing.T) {
	const privatePrefix = "synthetic-long-command-value"
	value := privatePrefix + strings.Repeat("z", 96)
	t.Setenv("DEVSY_TEST_TASKKILL_TOKEN", value)
	output := strings.Repeat("public ", 30) + value + strings.Repeat(" ordinary", 32)
	err := formatTaskkillDiagnostic(42, errors.New("fixture failure"), []byte(output))
	assert.NotContains(t, err.Error(), privatePrefix)
	assert.Contains(t, err.Error(), "***")
	assert.Contains(t, err.Error(), "ordinary")
	assert.True(t, strings.HasSuffix(err.Error(), "..."))
}

func TestCommandErrorMasksFoldedOutput(t *testing.T) {
	const fixture = "synthetic-command-continuation"
	exit := &exec.ExitError{
		Stderr: []byte("Authorization: Bearer\r\n " + fixture + "\r\npublic stderr"),
	}
	err := WrapCommandError([]byte("public stdout"), exit)
	require.ErrorIs(t, err, exit)
	assert.NotContains(t, err.Error(), fixture)
	assert.Contains(t, err.Error(), "public stdout")
	assert.Contains(t, err.Error(), "public stderr")
}

func TestWindowsCapturedOutputUsesDiagnosticFormatter(t *testing.T) {
	file, err := parser.ParseFile(
		token.NewFileSet(),
		"process_windows.go",
		nil,
		parser.SkipObjectResolution,
	)
	require.NoError(t, err)
	var paths int
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		captured, formatted := countWindowsDiagnosticCalls(function.Body)
		assert.Equal(
			t,
			captured,
			formatted,
			"%s must mask captures before formatting errors",
			function.Name.Name,
		)
		paths += captured
	}
	assert.GreaterOrEqual(t, paths, 2)
}

func countWindowsDiagnosticCalls(body *ast.BlockStmt) (captured, formatted int) {
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch target := call.Fun.(type) {
		case *ast.SelectorExpr:
			if target.Sel.Name == "CombinedOutput" {
				captured++
			}
		case *ast.Ident:
			if target.Name == "formatTaskkillDiagnostic" {
				formatted++
			}
		}
		return true
	})
	return captured, formatted
}
