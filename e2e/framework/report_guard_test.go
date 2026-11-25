package framework

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	redactPkgImport  = "github.com/devsy-org/devsy/internal/diagnosticredact"
	reportHelperFile = "framework/recordmask/record_masker.go"
	otherFile        = "tests/x.go"
)

func TestOnlyRecordMaskerCallsDiagnosticredactRecord(t *testing.T) {
	root := os.DirFS("..")
	var offenders []string
	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		src, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		found, err := scanSource(path, string(src))
		offenders = append(offenders, found...)
		return err
	})
	require.NoError(t, err)
	require.Empty(
		t,
		offenders,
		"call recordmask.ReportText instead of diagnosticredact.Record; dot imports of diagnosticredact are not allowed",
	)
}

func scanSource(path, src string) ([]string, error) {
	if path == reportHelperFile {
		return nil, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, pos := range recordCalls(file) {
		found = append(found, fset.Position(pos).String())
	}
	return found, nil
}

func TestRecordGuardScanSource(t *testing.T) {
	const header = "package x\n\nimport %s\"" + redactPkgImport + "\"\n\nfunc f() { %s }\n"
	cases := map[string]struct {
		path, imp, call string
		offending       bool
	}{
		"plain call":        {otherFile, "", "diagnosticredact.Record(nil, \"\")", true},
		"named alias call":  {otherFile, "dr ", "dr.Record(nil, \"\")", true},
		"dot import":        {otherFile, ". ", "Record(nil, \"\")", true},
		"unused dot import": {otherFile, ". ", "", true},
		"allowed file": {
			reportHelperFile,
			"",
			"diagnosticredact.Record(nil, \"\")",
			false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			found, err := scanSource(tc.path, fmt.Sprintf(header, tc.imp, tc.call))
			require.NoError(t, err)
			require.Equal(t, tc.offending, len(found) > 0, "%v", found)
		})
	}
}

func redactImport(file *ast.File) *ast.ImportSpec {
	for _, spec := range file.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path == redactPkgImport {
			return spec
		}
	}
	return nil
}

// A dot import hides Record behind a bare identifier, so it is rejected outright.
func recordCalls(file *ast.File) []token.Pos {
	spec := redactImport(file)
	if spec == nil {
		return nil
	}
	name := "diagnosticredact"
	if spec.Name != nil {
		name = spec.Name.Name
	}
	if name == "." {
		return []token.Pos{spec.Pos()}
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
