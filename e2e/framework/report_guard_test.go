package framework

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	redactPkgImport  = "github.com/devsy-org/devsy/internal/diagnosticredact"
	reportHelperFile = "framework/recordmask/record_masker.go"
)

func TestOnlyRecordMaskerCallsDiagnosticredactRecord(t *testing.T) {
	const root = ".."
	fset := token.NewFileSet()
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		if filepath.ToSlash(path) == "../"+reportHelperFile {
			return nil
		}
		for _, pos := range recordCalls(file) {
			offenders = append(offenders, fset.Position(pos).String())
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, offenders, "call recordmask.ReportText instead of diagnosticredact.Record")
}

func redactImportName(file *ast.File) string {
	for _, spec := range file.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path != redactPkgImport {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return "diagnosticredact"
	}
	return ""
}

func recordCalls(file *ast.File) []token.Pos {
	name := redactImportName(file)
	if name == "" {
		return nil
	}
	var calls []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Record" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == name {
			calls = append(calls, sel.Pos())
		}
		return true
	})
	return calls
}
